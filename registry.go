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

type RegistryCallable interface {
  Call(ctx context.Context, method string, args []json.RawMessage) (interface{}, *RPCError, *CallMetadata)
}

type Registry interface{
  RegistryCallable
  Register(namespace string, service interface{})
  RegisterMiddleware(Middleware)
}

func NewRegistry() Registry {
  reg := &registry{make(map[string]*callback), []Middleware{}}
  reg.Register("rpc", &registryApi{reg})
  return reg
}

type registry struct{
  callbacks map[string]*callback
  middleware []Middleware
}

type registryApi struct{
  registry *registry
}

func (api *registryApi) Modules() map[string]string {
  modules := make(map[string]string)
  for method := range api.registry.callbacks {
    modules[strings.Split(method, "_")[0]] = "1.0"
  }
  return modules
}

func (api *registryApi) Methods() []string {
  methods := make([]string, 0, len(api.registry.callbacks))
  for method := range api.registry.callbacks {
    methods = append(methods, method)
  }
  return methods
}

type callback struct{
  fn               reflect.Value
  takesContext     bool
  takesCallContext bool
  errIndex         int
  metaIndex        int
  argTypes         []reflect.Type
}

type CallMetadata struct{
  Hash     types.Hash
  Compute  *big.Int
  Duration time.Duration
}

func (cm *CallMetadata) AddCompute(x uint64) {
  if cm.Compute == nil {
    cm.Compute = new(big.Int).SetUint64(x)
  } else {
    cm.Compute.Add(cm.Compute, new(big.Int).SetUint64(x))
  }
}

func (cm *CallMetadata) AddBigCompute(x *big.Int) {
  if cm.Compute == nil {
    cm.Compute = new(big.Int).Set(x)
  } else {
    cm.Compute.Add(cm.Compute, x)
  }
}

type CallContext struct {
  ctx context.Context
  meta *CallMetadata
  data     map[string]interface{}
}

func (c *CallContext) Context() context.Context {
  return c.ctx
}

func (c *CallContext) Metadata() *CallMetadata {
  return c.meta
}


func (cm *CallContext) Set(key string, value interface{}) {
  if cm.data == nil { cm.data = make(map[string]interface{}) }
  cm.data[key] = value
}

func (cm *CallContext) Get(key string) (interface{}, bool) {
  if cm.data == nil { return nil, false }
  v, ok := cm.data[key]
  return v, ok
}

var (
  contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
  errorType = reflect.TypeOf((*error)(nil)).Elem()
  metaType = reflect.TypeOf((*CallMetadata)(nil))
  callContextType = reflect.TypeOf((*CallContext)(nil))
  healthStatusType = reflect.TypeOf(Healthy)
)

func (reg *registry) RegisterMiddleware(m Middleware) {
  reg.middleware = append(reg.middleware, m)
}

func (reg *registry) Register(namespace string, service interface{}) {
  receiver := reflect.ValueOf(service)
  receiverType := receiver.Type()
  METHOD_LOOP:
  for i := 0; i < receiverType.NumMethod(); i++ {
    meth := receiverType.Method(i)
    methVal := receiver.Method(i)
    methType := methVal.Type()
    takesContext := false
    takesCallContext := false
    errIndex := -1
    metaIndex := -1
    argTypes := []reflect.Type{}
    outtypes := []reflect.Type{}
    for j := 0; j < methType.NumIn(); j++ {
      if inType := methType.In(j); j == 0 && inType == contextType {
        takesContext = true
      } else if j == 0 && inType == callContextType {
        takesCallContext = true
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
      case healthStatusType:
        // Health checks should not be registered as callbacks
        continue METHOD_LOOP
      default:
        outtypes = append(outtypes, outType)
      }
    }
    reg.callbacks[rpcName(namespace, meth.Name)] = &callback{
      fn: methVal,
      takesContext: takesContext,
      takesCallContext: takesCallContext,
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
  cctx := &CallContext{ctx, &CallMetadata{}, nil}
  cb, ok := reg.callbacks[method]
  if !ok {
    return nil, NewRPCError(-32601, fmt.Sprintf("the method %v does not exist/is not available", method)), cm
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
  for i, m := range reg.middleware {
    res, err := m.Enter(cctx, method, args)
    if res != nil || err != nil {
      for j := i - 1; j >= 0 ; j-- {
        res, err = reg.middleware[j].Exit(cctx, res, err)
      }
      return res, err, cctx.meta
    }
  }
  argVals := []reflect.Value{}
  if cb.takesContext {
    argVals = append(argVals, reflect.ValueOf(cctx.ctx))
  } else if cb.takesCallContext {
    argVals = append(argVals, reflect.ValueOf(cctx))
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
    arg := reflect.New(argType)
    if err := json.Unmarshal(args[i], arg.Interface()); err != nil {
      return nil, NewRPCError(-32602, fmt.Sprintf("invalid argument %v: %v", i, err.Error())), cm
    }
    argVals = append(argVals, arg.Elem())
  }
  out := cb.fn.Call(argVals)
  if cb.metaIndex > -1 {
    var ok bool
    cm, ok = out[cb.metaIndex].Interface().(*CallMetadata)
    if !ok { cm = &CallMetadata{} }
  }
  var rpcErr *RPCError
  if cb.errIndex > -1 {
    if val := out[cb.errIndex]; !val.IsNil() {
      switch v := val.Interface().(type) {
      case RPCError:
        rpcErr = &v
      case *RPCError:
        rpcErr = v
      case error:
        rpcErr = NewRPCError(-1, v.Error())
      default:
        rpcErr = NewRPCError(-1, "An unknown error has occurred")
      }
    }
  }
  res = out[0].Interface()
  for i := len(reg.middleware) - 1; i >= 0 ; i-- {
    res, rpcErr = reg.middleware[i].Exit(cctx, res, rpcErr)
  }
  return res, rpcErr, cctx.meta
}

func rpcName(namespace, method string) string {
  return fmt.Sprintf("%v_%v%v", strings.ToLower(namespace), strings.ToLower(method[:1]), method[1:])
}
