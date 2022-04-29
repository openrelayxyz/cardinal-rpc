package rpc

import (
	"context"
	"encoding/json"
	"fmt"
	log "github.com/inconshreveable/log15"
	"github.com/openrelayxyz/cardinal-types"
	"github.com/openrelayxyz/cardinal-types/hexutil"
	"github.com/openrelayxyz/cardinal-types/metrics"
	gmetrics "github.com/rcrowley/go-metrics"
	"math/big"
	"reflect"
	"runtime"
	"strings"
	"time"
)

var (
	cmeter     = metrics.NewMajorMeter("/rpc/compute")
	calltimer  = metrics.NewMajorTimer("/rpc/timer")
	crashMeter = metrics.NewMajorCounter("/rpc/crash")
)

type RegistryCallable interface {
	Call(ctx context.Context, method string, args []json.RawMessage, output chan interface{}) (interface{}, *RPCError, *CallMetadata)
}

type Registry interface {
	RegistryCallable
	Register(namespace string, service interface{})
	RegisterMiddleware(Middleware)
	OnMissing(func(*CallContext, string, []json.RawMessage) (interface{}, *RPCError, *CallMetadata))
	Disconnect(context.Context)
}

func NewRegistry() Registry {
	reg := &registry{
		callbacks: make(map[string]*callback),
		subscriptions: make(map[string]*callback),
		middleware: []Middleware{},
		onMissing:handleMissing,
		subscriptionCounter: make(map[context.Context]hexutil.Uint64),
		subscriptionCancels: make(map[context.Context]map[hexutil.Uint64]func()),
	}
	reg.Register("rpc", &registryApi{reg})
	return reg
}

type registry struct {
	callbacks  map[string]*callback
	subscriptions map[string]*callback
	middleware []Middleware
	onMissing  func(*CallContext, string, []json.RawMessage) (interface{}, *RPCError, *CallMetadata)
	subscriptionCounter map[context.Context]hexutil.Uint64
	subscriptionCancels map[context.Context]map[hexutil.Uint64]func()
}

type registryApi struct {
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

type callback struct {
	fn               reflect.Value
	takesContext     bool
	takesCallContext bool
	errIndex         int
	metaIndex        int
	argTypes         []reflect.Type
	cmeter           gmetrics.Meter
	timer            gmetrics.Timer
}

type CallMetadata struct {
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

var (
	contextType      = reflect.TypeOf((*context.Context)(nil)).Elem()
	errorType        = reflect.TypeOf((*error)(nil)).Elem()
	metaType         = reflect.TypeOf((*CallMetadata)(nil))
	callContextType  = reflect.TypeOf((*CallContext)(nil))
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
		isSubscription := false
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
				if outType.Kind() == reflect.Chan {
					if (outType.ChanDir() & reflect.RecvDir) != reflect.RecvDir {
						log.Warn("Method returns non-receiving channel", "method", rpcName(namespace, meth.Name))
						continue METHOD_LOOP
					}
					isSubscription = true
				}
				outtypes = append(outtypes, outType)
			}
		}
		if !isSubscription {
			reg.callbacks[rpcName(namespace, meth.Name)] = &callback{
				fn:               methVal,
				takesContext:     takesContext,
				takesCallContext: takesCallContext,
				errIndex:         errIndex,
				metaIndex:        metaIndex,
				argTypes:         argTypes,
				cmeter:           metrics.NewMinorMeter(fmt.Sprintf("/rpc/%v/%v/compute", namespace, meth.Name)),
				timer:            metrics.NewMinorTimer(fmt.Sprintf("/rpc/%v/%v/timer", namespace, meth.Name)),
			}
		} else {
			reg.subscriptions[rpcName(namespace, meth.Name)] = &callback{
				fn:               methVal,
				takesContext:     takesContext,
				takesCallContext: takesCallContext,
				errIndex:         errIndex,
				metaIndex:        metaIndex,
				argTypes:         argTypes,
				cmeter:           metrics.NewMinorMeter(fmt.Sprintf("/rpc/%v/%v/compute", namespace, meth.Name)),
				timer:            metrics.NewMinorTimer(fmt.Sprintf("/rpc/%v/%v/timer", namespace, meth.Name)),
			}
		}
		log.Debug("Registered callback", "name", rpcName(namespace, meth.Name), "args", argTypes)
	}
}

type RPCError struct {
	C    int         `json:"code"`
	Msg  string      `json:"message"`
	Data interface{} `json:"data,omitempty"`
}

func (err *RPCError) Error() string {
	return err.Msg
}

func (err *RPCError) Code() int {
	return err.C
}

type rpcError interface {
	error
	ErrorCode() int
	ErrorData() interface{}
}

type hardEmpty struct {
	kind reflect.Kind
}

func (he hardEmpty) MarshalJSON() ([]byte, error) {
	switch he.kind {
	case reflect.Array, reflect.Slice:
		return []byte("[]"), nil
	case reflect.Map:
		return []byte("{}"), nil
	case reflect.String:
		return []byte(`""`), nil
	case reflect.Bool:
		return []byte("false"), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr, reflect.Float32, reflect.Float64:
		return []byte("0"), nil
	default:
		return []byte("null"), nil
	}
}

func NewRPCError(code int, msg string) *RPCError {
	return &RPCError{C: code, Msg: msg}
}

func emptyValue(v reflect.Value) (bool, reflect.Kind) {
	kind := v.Kind()
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0, kind
	case reflect.Bool:
		return !v.Bool(), kind
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0, kind
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0, kind
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0, kind
	case reflect.Interface, reflect.Ptr:
		return v.IsNil(), kind
	}
	return false, reflect.Invalid
}

func NewRPCErrorWithData(code int, msg string, data interface{}) *RPCError {
	return &RPCError{C: code, Msg: msg, Data: data}
}

func handleMissing(cctx *CallContext, method string, args []json.RawMessage) (interface{}, *RPCError, *CallMetadata) {
	return nil, NewRPCError(-32601, fmt.Sprintf("the method %v does not exist/is not available", method)), cctx.meta
}

func (reg *registry) OnMissing(fn func(*CallContext, string, []json.RawMessage) (interface{}, *RPCError, *CallMetadata)) {
	reg.onMissing = fn

}

func (reg *registry) Call(ctx context.Context, method string, args []json.RawMessage, outputs chan interface{}) (res interface{}, errRes *RPCError, cm *CallMetadata) {
	start := time.Now()
	cctx := &CallContext{ctx, &CallMetadata{}, nil}
	defer func() {
		calltimer.UpdateSince(start)
		if compute := cctx.Metadata().Compute; compute != nil {
			cmeter.Mark(compute.Int64())
		}
	}()
	if strings.HasSuffix(method, "_subscribe") {
		if outputs == nil {
			return nil, NewRPCError(-32000, "notifications not supported"), cctx.meta
		}
		return reg.subscribe(cctx, method, args, outputs)
	}
	if strings.HasSuffix(method, "_unsubscribe") {
		var subid hexutil.Uint64
		if err := json.Unmarshal(args[0], &subid); err != nil {
			return nil, NewRPCError(-32602, fmt.Sprintf("invalid argument %v: %v", 0, err.Error())), cctx.meta
		}
		if cancels, ok := reg.subscriptionCancels[ctx]; ok {
			if fn, ok := cancels[subid]; ok {
				fn()
				delete(cancels, subid)
			} else {
				return nil, NewRPCError(-32000, "subscription not found"), cctx.meta
			}
		} else {
			return nil, NewRPCError(-32000, "subscription not found"), cctx.meta
		}
		return true, nil, cctx.meta
	}
	defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			crashMeter.Inc(1)
			log.Error("RPC middleware handler crashed on " + method + " crashed: " + fmt.Sprintf("%v\n%s", err, buf))
			errRes = NewRPCError(-1, "method handler crashed")
			cm = cctx.meta
		}
	}()
	for _, m := range reg.middleware {
		defer func() {
			res, errRes = m.Exit(cctx, res, errRes)
		}()
		res, err := m.Enter(cctx, method, args)
		if res != nil || err != nil {
			return res, err, cctx.meta
		}
	}
	return reg.call(cctx, method, args)
}

func (reg *registry) parseArgs(cctx *CallContext, cb *callback, args []json.RawMessage) ([]reflect.Value, *RPCError) {
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
				return nil, NewRPCError(-32602, fmt.Sprintf("missing value for required argument %v", i))
			}
			argVals = append(argVals, reflect.Zero(argType))
			continue
		}
		arg := reflect.New(argType)
		if err := json.Unmarshal(args[i], arg.Interface()); err != nil {
			return nil, NewRPCError(-32602, fmt.Sprintf("invalid argument %v: %v", i, err.Error()))
		}
		argVals = append(argVals, arg.Elem())
	}
	return argVals, nil
}

func (reg *registry) Disconnect(ctx context.Context) {
	delete(reg.subscriptionCancels, ctx)
	delete(reg.subscriptionCounter, ctx)
}

func (reg *registry) subscribe(cctx *CallContext, method string, args []json.RawMessage, outputs chan interface{}) (res interface{}, errRes *RPCError, cm *CallMetadata) {
	namespace := strings.Split(method, "_")[0]
	var methodName string
	if err := json.Unmarshal(args[0], &methodName); err != nil {
		return nil, NewRPCError(-32602, fmt.Sprintf("invalid argument %v: %v", 0, err.Error())), cctx.meta
	}
	cb, ok := reg.subscriptions[fmt.Sprintf("%v_%v", namespace, methodName)]
	if !ok {
		return nil, NewRPCError(-32601, fmt.Sprintf(`no "%v" subscription in %v namespace`, methodName, namespace)), cctx.meta
	}
	subid := reg.subscriptionCounter[cctx.ctx]
	reg.subscriptionCounter[cctx.ctx]++
	if _, ok := reg.subscriptionCancels[cctx.ctx]; !ok {
		reg.subscriptionCancels[cctx.ctx] = make(map[hexutil.Uint64]func())
	}
	// TODO: On connection close, cleanup subscriptionCounter, and suscriptionCancels

	cctx.ctx, reg.subscriptionCancels[cctx.ctx][subid] = context.WithCancel(cctx.ctx)
	argVals, err := reg.parseArgs(cctx, cb, args[1:])
	if err != nil {
		return nil, err, cctx.meta
	}
	out := cb.fn.Call(argVals)
	var rpcErr *RPCError
	if cb.errIndex > -1 {
		if val := out[cb.errIndex]; !val.IsNil() {
			switch v := val.Interface().(type) {
			case RPCError:
				rpcErr = &v
			case *RPCError:
				rpcErr = v
			case rpcError:
				rpcErr = NewRPCErrorWithData(v.ErrorCode(), v.Error(), v.ErrorData())
			case error:
				rpcErr = NewRPCError(-1, v.Error())
			default:
				rpcErr = NewRPCError(-1, "An unknown error has occurred")
			}
		}
	}
	if rpcErr != nil {
		return nil, err, cctx.meta
	}
	go func(chVal reflect.Value, outputs chan interface{}, subid hexutil.Uint64) {
		for {
			item, ok := chVal.Recv()
			if !ok {
				return
			}
			response := &SubscriptionResponse{
				Version: "2.0",
				Method: method,
			}
			response.Params.ID = subid
			response.Params.Result = item.Interface()
			outputs <- response
		}
	}(out[0], outputs, subid)

	return subid, nil, cctx.meta
}

func (reg *registry) call(cctx *CallContext, method string, args []json.RawMessage) (res interface{}, errRes *RPCError, cm *CallMetadata) {
	cb, ok := reg.callbacks[method]
	if !ok {
		return reg.onMissing(cctx, method, args)
	}
	defer func(start time.Time) {
		cb.timer.UpdateSince(start)
		if compute := cctx.Metadata().Compute; compute != nil {
			cb.cmeter.Mark(compute.Int64())
		}
	}(time.Now())
	defer func() {
		if err := recover(); err != nil {
			const size = 64 << 10
			buf := make([]byte, size)
			buf = buf[:runtime.Stack(buf, false)]
			crashMeter.Inc(1)
			log.Error("RPC method " + method + " crashed: " + fmt.Sprintf("%v\n%s", err, buf))
			errRes = NewRPCError(-1, "method handler crashed")
			cm = cctx.meta
		}
	}()
	argVals, err := reg.parseArgs(cctx, cb, args)
	if err != nil {
		return nil, err, cctx.meta
	}
	out := cb.fn.Call(argVals)
	if cb.metaIndex > -1 {
		var ok bool
		cm, ok = out[cb.metaIndex].Interface().(*CallMetadata)
		if !ok {
			cm = &CallMetadata{}
		}
	}
	var rpcErr *RPCError
	if cb.errIndex > -1 {
		if val := out[cb.errIndex]; !val.IsNil() {
			switch v := val.Interface().(type) {
			case RPCError:
				rpcErr = &v
			case *RPCError:
				rpcErr = v
			case rpcError:
				rpcErr = NewRPCErrorWithData(v.ErrorCode(), v.Error(), v.ErrorData())
			case error:
				rpcErr = NewRPCError(-1, v.Error())
			default:
				rpcErr = NewRPCError(-1, "An unknown error has occurred")
			}
		}
	}
	res = out[0].Interface()
	if empty, kind := emptyValue(out[0]); empty && rpcErr == nil {
		res = hardEmpty{kind}
	}
	return res, rpcErr, cctx.meta
}

func rpcName(namespace, method string) string {
	return fmt.Sprintf("%v_%v%v", strings.ToLower(namespace), strings.ToLower(method[:1]), method[1:])
}
