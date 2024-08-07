package rpc

import (
  "encoding/json"
  "context"
  "fmt"
  "math"
  "sync"
  "sync/atomic"
  "github.com/openrelayxyz/cardinal-types/hexutil"
  // log "github.com/inconshreveable/log15"
  "strings"
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

type SubscriptionResponse struct {
  Version string          `json:"jsonrpc"`
  Method  string          `json:"method"`
  Params  struct{
    ID hexutil.Uint64 `json:"subscription"`
    Result interface{} `json:"result"`
  } `json:"params"`
}
type SubscriptionResponseRaw struct {
  Version string          `json:"jsonrpc"`
  Method  string          `json:"method"`
  Params  struct{
    ID hexutil.Uint64 `json:"subscription"`
    Result json.RawMessage `json:"result"`
  } `json:"params"`
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
  Latest int64
  Await  func(int64) bool
}

func NewContext(cx context.Context) *CallContext {
  return &CallContext{
    ctx: cx,
    meta: &CallMetadata{},
    data: make(map[string]interface{}),
  }
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


type latestUnmarshaller struct {
  lock *sync.Mutex
	latestList []*BlockNumber
	safeList []*BlockNumber
	finalizedList []*BlockNumber
}

func (lm *latestUnmarshaller) Add(b *BlockNumber) {
	lm.latestList = append(lm.latestList, b)
}
func (lm *latestUnmarshaller) AddFinalized(b *BlockNumber) {
	lm.finalizedList = append(lm.finalizedList, b)
}
func (lm *latestUnmarshaller) AddSafe(b *BlockNumber) {
	lm.safeList = append(lm.safeList, b)
}

func (lm *latestUnmarshaller) Resolve(await func(int64) bool, finalized, safe, latest int64) {
	ll := lm.latestList
	lm.latestList = []*BlockNumber{}
	fl := lm.finalizedList
	lm.finalizedList = []*BlockNumber{}
	sl := lm.safeList
	lm.safeList = []*BlockNumber{}
	lm.lock.Unlock()
	if len(ll) > 0 && await(latest) {
		for _, p := range ll {	
			if p != nil {
				*p = BlockNumber(latest)
			}
		}
	}
	// We don't need to await for safe or finalized, since those are older block numbers anyway.
	for _, p := range fl {
		if p != nil {
			*p = BlockNumber(finalized)
		}
	}
	for _, p := range sl {
		if p != nil {
			*p = BlockNumber(safe)
		}
	}
}

var lm *latestUnmarshaller

func init() {
  lm = &latestUnmarshaller{lock: &sync.Mutex{}, latestList: []*BlockNumber{}}
}

func (lm *latestUnmarshaller) Unmarshal(data []byte, value interface{}, finalized, safe, latest int64, await func(int64) bool) error {
  if latest != -1 {
		lm.lock.Lock()
		defer lm.Resolve(await, finalized, safe, latest)
  }
  return json.Unmarshal(data, value)
}


type BlockNumber int64

const (
  FinalizedBlockNumber = BlockNumber(-4)
  SafeBlockNumber      = BlockNumber(-3)
  PendingBlockNumber   = BlockNumber(-2)
  LatestBlockNumber    = BlockNumber(-1)
  EarliestBlockNumber  = BlockNumber(0)
)

func (bn *BlockNumber) UnmarshalJSON(data []byte) error {
  v := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(string(data)), `"`), `"`)

  switch v {
  case "earliest":
    *bn = EarliestBlockNumber
    return nil
  case "latest":
		lm.Add(bn)
    *bn = LatestBlockNumber
    return nil
  case "pending":
    *bn = PendingBlockNumber
    return nil
	case "safe":
		lm.AddSafe(bn)
		*bn = SafeBlockNumber
		return nil
	case "finalized":
		lm.AddFinalized(bn)
		*bn = FinalizedBlockNumber
		return nil
  }

  n, err := hexutil.DecodeUint64(v)
  if err != nil {
    return err
  }
  if n > math.MaxInt64 {
    return fmt.Errorf("block number larger than int64")
  }
  *bn = BlockNumber(n)
  return nil
}

func (bn BlockNumber) MarshalJSON() ([]byte, error) {
	switch bn {
	case -4:
		return []byte(`"finalized"`), nil
	case -3:
		return []byte(`"safe"`), nil
	case -2:
		return []byte(`"pending"`), nil
	case -1:
		return []byte(`"latest"`), nil
	default:
		return json.Marshal(hexutil.Uint(bn))
	}
}

type HeightRecord struct {
	Latest    int64
	Safe      *int64
	Finalized *int64
}