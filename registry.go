package rpc

import (
  "context"
  "encoding/json"
  "fmt"
  "math/big"
  "reflect"
  "runtime"
  "strings"
  "time"
  "github.com/openrelayxyz/cardinal-types"
  log "github.com/inconshreveable/log15"
)

type Registry interface{
  Call(ctx context.Context, method string, args []json.RawMessage) (interface{}, *RPCError, *CallMetadata)
  Register(namespace string, service interface{})
}

func NewRegistry() Registry {
  return &registry{make(map[string]*callback)}
}

type registry struct{
  callbacks map[string]*callback
}

type callback struct{
  fn           reflect.Value
  takesContext bool
  errIndex     int
  metaIndex    int
  argTypes     []reflect.Type
}

type CallMetadata struct{
  Hash     types.Hash
  Compute  *big.Int
  Duration time.Duration
}

var (
  contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
  errorType = reflect.TypeOf((*error)(nil)).Elem()
  metaType = reflect.TypeOf((*CallMetadata)(nil)).Elem()
)

func (reg *registry) Register(namespace string, service interface{}) {
  receiver := reflect.ValueOf(service)
  receiverType := receiver.Type()
  for i := 0; i < receiverType.NumMethod(); i++ {
    meth := receiverType.Method(i)
    methVal := receiver.Method(i)
    methType := methVal.Type()
    takesContext := false
    errIndex := -1
    metaIndex := -1
    argTypes := []reflect.Type{}
    outtypes := []reflect.Type{}
    for j := 0; j < methType.NumIn(); j++ {
      if inType := methType.In(j); j == 0 && inType == contextType {
        takesContext = true
      } else {
        argTypes = append(argTypes, inType)
      }
    }
    for j := 0; j < methType.NumOut(); j++ {
      switch outType := methType.Out(j); outType {
      case errorType:
        errIndex = j
      case metaType:
        metaIndex = j
      default:
        outtypes = append(outtypes, outType)
      }
    }
    reg.callbacks[rpcName(namespace, meth.Name)] = &callback{
      fn: methVal,
      takesContext: takesContext,
      errIndex: errIndex,
      metaIndex: metaIndex,
      argTypes: argTypes,
    }
    log.Debug("Registered callback", "name", rpcName(namespace, meth.Name), "args", argTypes)
  }
}

type RPCError struct{
  C int    `json:"code"`
  Msg  string `json:"message"`
}

func (err *RPCError) Error() string {
  return err.Msg
}

func (err *RPCError) Code() int {
  return err.C
}

func NewRPCError(code int, msg string) *RPCError {
  return &RPCError{C: code, Msg:msg}
}

func (reg *registry) Call(ctx context.Context, method string, args []json.RawMessage) (res interface{}, errRes *RPCError, cm *CallMetadata) {
  cm = &CallMetadata{}
  cb, ok := reg.callbacks[method]
  if !ok {
    return nil, NewRPCError(-32601, fmt.Sprintf("the method %v does not exist/is not available", method)), cm
  }
  argVals := []reflect.Value{}
  if cb.takesContext {
    argVals = append(argVals, reflect.ValueOf(ctx))
  }
  for i, argType := range cb.argTypes {
    t := argType
    derefs := 0
    for t.Kind() == reflect.Ptr {
      t = t.Elem()
      derefs++
    }
    if len(args) <= i {
      if derefs == 0 {
        return nil, NewRPCError(-32602, fmt.Sprintf("missing value for required argument %v", i)), cm
      }
      argVals = append(argVals, reflect.Zero(argType))
      continue
    }
    arg := reflect.New(t)
    if err := json.Unmarshal(args[i], arg.Interface()); err != nil {
      return nil, NewRPCError(-32602, fmt.Sprintf("invalid argument %v: %v", i, err.Error())), cm
    }
    argVals = append(argVals, arg.Elem())
  }
  defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			log.Error("RPC method " + method + " crashed: " + fmt.Sprintf("%v\n%s", err, buf))
			errRes = NewRPCError(-1, "method handler crashed")
		}
	}()
  out := cb.fn.Call(argVals)
  if cb.metaIndex > -1 {
    var ok bool
    cm, ok = out[cb.metaIndex].Interface().(*CallMetadata)
    if !ok { cm = &CallMetadata{} }
  }
  if cb.errIndex > -1 {
    if val := out[cb.errIndex]; !val.IsNil() {
      switch v := val.Interface().(type) {
      case RPCError:
        return nil, &v, cm
      case *RPCError:
        return nil, v, cm
      case error:
        return nil, NewRPCError(-1, v.Error()), cm
      default:
        return nil, NewRPCError(-1, "An unknown error has occurred"), cm
      }
    }
  }
  return out[0].Interface(), nil, cm
}

func rpcName(namespace, method string) string {
  return fmt.Sprintf("%v_%v%v", strings.ToLower(namespace), strings.ToLower(method[:1]), method[1:])
}
