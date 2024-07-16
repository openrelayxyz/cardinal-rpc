package rpc

import (
  "context"
  "encoding/json"
  "fmt"
  "testing"
  "strconv"
  "time"
  "reflect"
  "math/big"
  log "github.com/inconshreveable/log15"
)

type testService struct{}

func (t *testService) Hello() string {
  return "Hello World"
}

func (t *testService) NilInterface() interface{} {
  return nil
}

func (t *testService) EmptyString() string {
  return ""
}

func (t *testService) EmptyList() []string {
  return []string{}
}

func (t *testService) EmptyMap() map[string]string {
  return make(map[string]string)
}

func (t *testService) BlockNumber(latest BlockNumber) string {
  return strconv.Itoa(int(latest))

}
func (t *testService) MathBig(b *big.Int) string {
  return b.String()
}

func (t *testService) Delay(cctx *CallContext) bool {
	return cctx.Await(cctx.Latest)
}

type istruct struct {
	a int
	B int
}

func (t *testService) StructInput(i istruct) istruct {
	return i
}

func (t *testService) FooBar(foo string, bar int) (string, error) {
  if bar < 0 {
    return "", fmt.Errorf("Argument two cannot be negative")
  }
  return fmt.Sprintf("Hello World %v %v", foo, bar), nil
}

func (t *testService) Panic(a string, b int) (error) {
  panic("Oh no!")
}

func TestRegister(t *testing.T) {
  registry := NewRegistry(16)
  registry.Register("test", &testService{})
  out, err, _ := registry.Call(context.Background(), "test_hello", []json.RawMessage{}, nil, -1)
  if err != nil { t.Errorf(err.Error()) }
  v, ok := out.(string)
  if !ok { t.Errorf("Expected type") }
  if v != "Hello World" { t.Errorf("Unexpected output") }
}

// func TestSubscription(t *testing.T) {
//   registry := NewRegistry(16)
//   c := make(chan int, 1)
//   outputs := make(chan []byte, 200)
//   // TODO: Implement subscriptionTest with GetItem method that pulls from c
//   registry.Register("test", &subscriptionTest{c})
//   ctx, cancel := context.WithCancel(context.Background())
//   defer cancel()
//   out, err, _ := registry.Call(ctx, "test_subscribe", []json.RawMessage{json.RawMessage(`"getItem"`)}, outputs, -1)
//   // TODO: Check that err == nil, out has expected values
//   c <- 5
//   result := <-outputs
//   // Check that result has a value of 5
// }

func TestMissing(t *testing.T) {
  registry := NewRegistry(16)
  _, err, _ := registry.Call(context.Background(), "test_hello", []json.RawMessage{}, nil, -1)
  if err == nil { t.Errorf("expected method to be missing") }
  if code := err.Code(); code != -32601 {
    t.Errorf("Expected error code -32601, got %v", code)
  }
}
func TestCustomMissing(t *testing.T) {
  registry := NewRegistry(16)
  registry.OnMissing(func(cctx *CallContext, method string, args []json.RawMessage) (interface{}, *RPCError, *CallMetadata) {
    return "Woops 404", nil, cctx.meta
  })
  out, err, _ := registry.Call(context.Background(), "test_hello", []json.RawMessage{}, nil, -1)
  if err != nil { t.Errorf(err.Error()) }
  v, ok := out.(string)
  if !ok { t.Errorf("Expected type") }
  if v != "Woops 404" { t.Errorf("Unexpected output") }
}

func TestCallComplex(t *testing.T) {
  registry := NewRegistry(16)
  registry.Register("test", &testService{})
  out, err, _ := registry.Call(context.Background(), "test_fooBar", []json.RawMessage{
    json.RawMessage(`"Foo"`),
    json.RawMessage("13"),
  }, nil, -1)
  if err != nil { t.Errorf(err.Error()) }
  v, ok := out.(string)
  if !ok { t.Errorf("Expected type") }
  if v != "Hello World Foo 13" { t.Errorf("Unexpected output") }
}

func TestCallBn(t *testing.T) {
  registry := NewRegistry(16)
  registry.Register("test", &testService{})
  log.Debug("test_blockNumber", "param", "latest", "latest", 3)
  out, err, _ := registry.Call(context.Background(), "test_blockNumber", []json.RawMessage{json.RawMessage(`"latest"`)}, nil, 3)
  if err != nil { t.Errorf(err.Error()) }
  v, ok := out.(string)
  if !ok { t.Errorf("Expected type") }
  if v != "3" { t.Errorf("Unexpected output: %v", v) }
  hf := make(chan *HeightRecord)
  registry.RegisterHeightRecordFeed(hf)
  hf <- &HeightRecord{Latest: 50}
  time.Sleep(20 * time.Millisecond)
  start := time.Now()
  go func() {
    time.Sleep(20 * time.Millisecond)
    hf <- &HeightRecord{Latest: 51}
  }()
  log.Debug("test_blockNumber", "param", "0x33", "latest", 3)
  out2, err2, _ := registry.Call(context.Background(), "test_blockNumber", []json.RawMessage{json.RawMessage(`"0x33"`)}, nil, 3)
  if d := time.Since(start); d > 400 * time.Millisecond {
    t.Errorf("Call time took too long: %v", d)
  } else if d < 20 * time.Millisecond {
    t.Errorf("Call time was too fast: %v", d)
  }
  if err2 != nil { t.Errorf(err2.Error()) }
  v2, ok := out2.(string)
  if !ok { t.Errorf("Expected type") }
  if v2 != "51" { t.Errorf("Unexpected output, wanted '51' got %v", v2) }
	start = time.Now()
	go func() {
    time.Sleep(20 * time.Millisecond)
    hr := &HeightRecord{
      Latest: 55,
      Safe: new(int64),
      Finalized: new(int64),
    }
    *hr.Safe = 20
    hf <- hr
  }()
	log.Debug("test_delay", "latest", 52)
	v3, err3, _ := registry.Call(context.Background(), "test_delay", []json.RawMessage{}, nil, 52)
	if d := time.Since(start); d > 400 * time.Millisecond {
    t.Errorf("Call time took too long: %v", d)
  } else if d < 20 * time.Millisecond {
    t.Errorf("Call time was too fast: %v", d)
  }
	if err3 != nil { t.Errorf(err3.Error()) }
	if v3 != true {
		t.Errorf("Unexpected value: %v", v3)
	}
	result, err4, _ := registry.Call(context.Background(), "test_structInput", []json.RawMessage{json.RawMessage(`{"B": 3}`)}, nil, 52)
	if result.(istruct).B != 3 {
		t.Errorf("Unexpected value")
	}
	if err4 != nil { t.Errorf(err4.Error()) }
	_, err5, _ := registry.Call(context.Background(), "test_mathBig", []json.RawMessage{json.RawMessage(`5`)}, nil, 52)
	if err5 != nil { t.Errorf(err5.Error()) }
	out6, err6, _ := registry.Call(context.Background(), "test_blockNumber", []json.RawMessage{json.RawMessage(`"latest"`)}, nil, 60)
  if err != nil { t.Errorf(err6.Error()) }
  v6, ok := out6.(string)
  if !ok { t.Errorf("Expected type") }
  if v6 != "-1" { t.Errorf("Unexpected output: %v", v6) } // 60 is far enough in the future we don't expect that to resolve

	out7, err7, _ := registry.Call(context.Background(), "test_blockNumber", []json.RawMessage{json.RawMessage(`"safe"`)}, nil, 56)
  if err != nil { t.Errorf(err7.Error()) }
  v7, ok := out7.(string)
  if !ok { t.Errorf("Expected type") }
  if v7 != "20" { t.Errorf("Unexpected output: %v", v7) }
}

func TestCollectItem(t *testing.T) {
  x := collect[BlockNumber](BlockNumber(6))
  if len(x) != 1 {
    t.Errorf("Unexpected count")
  }
  if x[0] != 6 {
    t.Errorf("Unexpected value")
  }
}

func TestCollectFromStruct(t *testing.T) {
  type q struct {
    X *BlockNumber
  }
  p := BlockNumber(6)
  w := q{X: &p}
  x := collect[BlockNumber](&w)
  if len(x) != 1 {
    t.Fatalf("Unexpected count: %v", x)
  }
  if x[0] != 6 {
    t.Errorf("Unexpected value")
  }

}

type nilTest struct {
  call string
  kind reflect.Kind
  render string
}

var nilTests = []nilTest{
  {"test_nilInterface", reflect.Interface, "null"},
  {"test_emptyString", reflect.String, `""`},
  {"test_emptyList", reflect.Slice, "[]"},
  {"test_emptyMap", reflect.Map, "{}"},
}


func TestCallNilResult(t *testing.T) {
  registry := NewRegistry(16)
  registry.Register("test", &testService{})
  for _, nt := range nilTests {
    t.Run(nt.call, func(t *testing.T) {
      out, err, _ := registry.Call(context.Background(), nt.call, []json.RawMessage{}, nil, -1)
      if err != nil { t.Errorf(err.Error()) }
      v, ok := out.(hardEmpty)
      if !ok { t.Errorf("Expected type to be hardEmpty") }
      if v.kind != nt.kind { t.Errorf("Unexpected kind. Expected %v, got %v", nt.kind, v.kind) }
      data, e := json.Marshal(out)
      if e != nil { t.Errorf(e.Error()) }
      if string(data) != nt.render {
        t.Errorf("Unexpected JSON marshal. Expected %v, got %v", nt.render, string(data))
      }
    })
  }
}

func TestCallErrors(t *testing.T) {
  registry := NewRegistry(16)
  registry.Register("test", &testService{})
  _, err, _ := registry.Call(context.Background(), "test_fooBar", []json.RawMessage{
    json.RawMessage(`"Foo"`),
    json.RawMessage("-13"),
  }, nil, -1)
  if err == nil { t.Errorf("Expected error, got none") }
}

func TestPanic(t *testing.T) {
  registry := NewRegistry(16)
  registry.Register("test", &testService{})
  _, err, _ := registry.Call(context.Background(), "test_panic", []json.RawMessage{[]byte(`"foo"`), []byte("3")}, nil, -1)
  if err == nil { t.Errorf("Expected error, got none") }
  log.Info("The above stack trace is not indicative of a problem. We're testing that panics get handled properly, and part of that is logging the panic.")
}

type helloMW struct{}

func (*helloMW) Enter(cctx *CallContext, method string, args []json.RawMessage) (interface{}, *RPCError) {
  return "Hello Middleware", nil
}

func (*helloMW) Exit(cctx *CallContext, result interface{}, err *RPCError) (interface{}, *RPCError) {
  return result, err
}

func TestSimpleMiddleware(t *testing.T) {
  registry := NewRegistry(16)
  registry.Register("test", &testService{})
  registry.RegisterMiddleware(&helloMW{})
  out, err, _ := registry.Call(context.Background(), "test_panic", []json.RawMessage{}, nil, -1)
  if err != nil { t.Errorf(err.Error()) }
  v, ok := out.(string)
  if !ok { t.Errorf("Expected type") }
  if v != "Hello Middleware" { t.Errorf("Unexpected output") }
}

type exitMW struct{}

func (*exitMW) Enter(cctx *CallContext, method string, args []json.RawMessage) (interface{}, *RPCError) {
  return nil, nil
}

func (*exitMW) Exit(cctx *CallContext, result interface{}, err *RPCError) (interface{}, *RPCError) {
  if err != nil {
    return err.Error(), nil
  } else {
    return nil, nil
  }
}

func TestExitMiddleware(t *testing.T) {
  registry := NewRegistry(16)
  registry.Register("test", &testService{})
  registry.RegisterMiddleware(&exitMW{})
  out, err, _ := registry.Call(context.Background(), "test_fooBar", []json.RawMessage{
    json.RawMessage(`"Foo"`),
    json.RawMessage("-13"),
  }, nil, -1)
  if err != nil { t.Errorf("Unexpected error") }
  v, ok := out.(string)
  if !ok { t.Errorf("Expected type") }
  if v != "Argument two cannot be negative" { t.Errorf("Unexpected output") }
}
