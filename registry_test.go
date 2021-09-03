package rpc

import (
  "context"
  "encoding/json"
  "fmt"
  "testing"
  log "github.com/inconshreveable/log15"
)

type testService struct{}

func (t *testService) Hello() string {
  return "Hello World"
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