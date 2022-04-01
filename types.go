package rpc

import (
  "encoding/json"
  "context"
  "sync/atomic"
)

type Call struct {
  Version string            `json:"jsonrpc"`
  ID      json.RawMessage   `json:"id"`
  Method  string            `json:"method"`
  Params  []json.RawMessage `json:"params"`
}

var globalId int64

func toRawMessages(items ...interface{}) ([]json.RawMessage, error) {
  result := make([]json.RawMessage, len(items))
  for i, item := range items {
    d, err := json.Marshal(item)
    if err != nil { return nil, err }
    result[i] = (json.RawMessage)(d)
  }
  return result, nil
}

func NewCall(method string, params ...interface{}) (*Call, error) {
  rawparams, err := toRawMessages(params...)
  if err != nil { return nil, err }
  return NewCallParams(method, rawparams)
}

func NewCallParams(method string, rawparams []json.RawMessage) (*Call, error) {
  id, err := toRawMessages(atomic.AddInt64(&globalId, 1))
  if err != nil { return nil, err }
  return &Call{
    Version: "2.0",
    ID : id[0],
    Method: method,
    Params: rawparams,
  }, nil
}

type Response struct{
  Version string          `json:"jsonrpc"`
  ID      json.RawMessage `json:"id"`
  Error   *RPCError       `json:"error,omitempty"`
  Result  interface{}     `json:"result,omitempty"`
  Params  interface{}     `json:"params,omitempty"`
  Meta    *CallMetadata   `json:"-"`
}

type RawResponse struct{
  Version string          `json:"jsonrpc"`
  ID      json.RawMessage `json:"id"`
  Error   *RPCError       `json:"error,omitempty"`
  Result  json.RawMessage `json:"result,omitempty"`
  Params  json.RawMessage `json:"params,omitempty"`
  Meta    *CallMetadata   `json:"-"`
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
