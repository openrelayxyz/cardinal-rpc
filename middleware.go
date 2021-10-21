package rpc

import (
  "encoding/json"
)

type Middleware interface{
  Enter(*CallContext, string, []json.RawMessage) (interface{}, *RPCError)
  Exit(*CallContext, interface{}, *RPCError) (interface{}, *RPCError)
}
