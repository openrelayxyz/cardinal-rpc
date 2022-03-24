package rpc

import (
  "context"
  "encoding/json"
  "fmt"
  "testing"
  "reflect"
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

func (t *testService) FooBar(foo string, bar int) (string, error) {
  if bar < 0 {
    return "", fmt.Errorf("Argument two cannot be negative")
  }
  return fmt.Sprintf("Hello World %v %v", foo, bar), nil
}

func (t *testService) Panic() (error) {
  panic("Oh no!")
}

func TestRegister(t *testing.T) {
  registry := NewRegistry()
  registry.Register("test", &testService{})
  out, err, _ := registry.Call(context.Background(), "test_hello", []json.RawMessage{})
  if err != nil { t.Errorf(err.Error()) }
  v, ok := out.(string)
  if !ok { t.Errorf("Expected type") }
  if v != "Hello World" { t.Errorf("Unexpected output") }
}

func TestMissing(t *testing.T) {
  registry := NewRegistry()
  _, err, _ := registry.Call(context.Background(), "test_hello", []json.RawMessage{})
  if err == nil { t.Errorf("expected method to be missing") }
  if code := err.Code(); code != -32601 {
    t.Errorf("Expected error code -32601, got %v", code)
  }
}
func TestCustomMissing(t *testing.T) {
  registry := NewRegistry()
  registry.OnMissing(func(cctx *CallContext, method string, args []json.RawMessage) (interface{}, *RPCError, *CallMetadata) {
    return "Woops 404", nil, cctx.meta
  })
  out, err, _ := registry.Call(context.Background(), "test_hello", []json.RawMessage{})
  if err != nil { t.Errorf(err.Error()) }
  v, ok := out.(string)
  if !ok { t.Errorf("Expected type") }
  if v != "Woops 404" { t.Errorf("Unexpected output") }
}

func TestCallComplex(t *testing.T) {
  registry := NewRegistry()
  registry.Register("test", &testService{})
  out, err, _ := registry.Call(context.Background(), "test_fooBar", []json.RawMessage{
    json.RawMessage(`"Foo"`),
    json.RawMessage("13"),
  })
  if err != nil { t.Errorf(err.Error()) }
  v, ok := out.(string)
  if !ok { t.Errorf("Expected type") }
  if v != "Hello World Foo 13" { t.Errorf("Unexpected output") }
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
  registry := NewRegistry()
  registry.Register("test", &testService{})
  for _, nt := range nilTests {
    t.Run(nt.call, func(t *testing.T) {
      out, err, _ := registry.Call(context.Background(), nt.call, []json.RawMessage{})
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
  registry := NewRegistry()
  registry.Register("test", &testService{})
  _, err, _ := registry.Call(context.Background(), "test_fooBar", []json.RawMessage{
    json.RawMessage(`"Foo"`),
    json.RawMessage("-13"),
  })
  if err == nil { t.Errorf("Expected error, got none") }
}

func TestPanic(t *testing.T) {
  registry := NewRegistry()
  registry.Register("test", &testService{})
  _, err, _ := registry.Call(context.Background(), "test_panic", []json.RawMessage{})
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
  registry := NewRegistry()
  registry.Register("test", &testService{})
  registry.RegisterMiddleware(&helloMW{})
  out, err, _ := registry.Call(context.Background(), "test_panic", []json.RawMessage{})
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
  registry := NewRegistry()
  registry.Register("test", &testService{})
  registry.RegisterMiddleware(&exitMW{})
  out, err, _ := registry.Call(context.Background(), "test_fooBar", []json.RawMessage{
    json.RawMessage(`"Foo"`),
    json.RawMessage("-13"),
  })
  if err != nil { t.Errorf("Unexpected error") }
  v, ok := out.(string)
  if !ok { t.Errorf("Expected type") }
  if v != "Argument two cannot be negative" { t.Errorf("Unexpected output") }
}
