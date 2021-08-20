package transports

import
(
  "context"
  "encoding/json"
  "fmt"
  "math/big"
  "math/rand"
  "net/http"
  "github.com/openrelayxyz/cardinal-rpc"
  "github.com/openrelayxyz/cardinal-types"
  "github.com/NYTimes/gziphandler"
  "github.com/rs/cors"
  "strings"
  "time"
  "io/ioutil"
  log "github.com/inconshreveable/log15"
)


type httpTransport struct {
  port int64
  semaphore chan struct{}
  s *http.Server
  running bool
  registry rpc.Registry
}

func NewHTTPTransport(port int64, semaphore chan struct{}, registry rpc.Registry) Transport {
  return &httpTransport{
    port: port,
    semaphore: semaphore,
    registry: registry,
  }
}

type rpcCall struct {
  Version string            `json:"jsonrpc"`
  ID      json.RawMessage   `json:"id"`
  Method  string            `json:"method"`
  Params  []json.RawMessage `json:"params"`
}

type rpcResponse struct{
  Version string          `json:"jsonrpc"`
  ID      json.RawMessage `json:"id"`
  Error   *rpc.RPCError   `json:"error,omitempty"`
  Result  interface{}     `json:"result,omitempty"`
  Params  interface{}     `json:"params,omitempty"`
  meta    *rpc.CallMetadata
}

func handleError(w http.ResponseWriter, err error) {
  w.WriteHeader(400)
  response, _ := json.Marshal(&rpcResponse{
    Version: "2.0",
    ID: json.RawMessage("-1"),
    Error: rpc.NewRPCError(-1, err.Error()),
  })
  w.Write(response)
}
func writeResponse(w http.ResponseWriter, result interface{}) {
  switch v := result.(type) {
  case *rpcResponse:
    w.Header().Set("X-Response-Time", fmt.Sprintf("%vns", v.meta.Duration.Nanoseconds()))
    if v.meta.Hash != (types.Hash{}) {
      w.Header().Set("X-Hash", fmt.Sprintf("%#x", v.meta.Hash))
    }
    if v.meta.Compute != nil {
      w.Header().Set("X-Compute-Cost", v.meta.Compute.String())
    }
  case []rpcResponse:
    totalDuration := int64(0)
    hashes := map[string]struct{}{}
    totalCompute := new(big.Int)
    for _, call := range v {
      totalDuration += call.meta.Duration.Nanoseconds()
      if call.meta.Hash != (types.Hash{}) { hashes[call.meta.Hash.Hex()] = struct{}{} }
      if call.meta.Compute != nil { totalCompute.Add(totalCompute, call.meta.Compute) }
    }
    w.Header().Set("X-Response-Time", fmt.Sprintf("%vns", totalDuration))
    if len(hashes) > 0 {
      hashSlice := make([]string, 0, len(hashes))
      for hash := range hashes { hashSlice = append(hashSlice, hash) }
      w.Header().Set("X-Hash", strings.Join(hashSlice, ","))
    }
    if totalCompute.Cmp(new(big.Int)) != 0 {
      w.Header().Set("X-Compute-Cost", totalCompute.String())
    }
  default:
  }
  w.WriteHeader(200)
  response, _ := json.Marshal(result)
  w.Write(response)
  w.Write([]byte("\n"))
}

func (t *httpTransport) Start(failure chan error) error {
  if t.s != nil {
    return fmt.Errorf("httpTransport already started")
  }
  rand.Seed(time.Now().UnixNano())
  mux := http.NewServeMux()
  mux.HandleFunc("/", t.handleFunc)
  t.s = &http.Server{
    Addr: fmt.Sprintf(":%v", t.port),
    Handler: gziphandler.GzipHandler(cors.Default().Handler(mux)),
    ReadHeaderTimeout: 5 * time.Second,
    MaxHeaderBytes: 1 << 20,
  }
  go func() { failure <- t.s.ListenAndServe() }()
  log.Info("Running http transport", "port", t.port)
  t.running = true
  return nil
}

func (s *httpTransport) Stop() error {
  s.running = false
  return s.s.Shutdown(context.Background())
}

func (t *httpTransport) handleFunc(w http.ResponseWriter, r *http.Request) {
  w.Header().Set("Content-Type", "application/json")
  if r.Method != "POST" {
    w.WriteHeader(200)
    w.Write([]byte("{}"))
    return
  }
  call := &rpcCall{}
  body, err := ioutil.ReadAll(r.Body)
  if err != nil { return } // TODO: Handle response
  if err := json.Unmarshal(body, call); err == nil {
    response := t.handleSingle(r.Context(), call)
    writeResponse(w, response)
    return
  }
  calls := []rpcCall{}
  if err := json.Unmarshal(body, &calls); err == nil {
    response := t.handleBatch(r.Context(), calls)
    writeResponse(w, response)
    return
  } else {
    handleError(w, err)
  }
}

func (t *httpTransport) handleSingle(ctx context.Context, call *rpcCall) *rpcResponse {
  t.semaphore <- struct{}{}
  start := time.Now()
  result, err, meta := t.registry.Call(ctx, call.Method, call.Params)
  <-t.semaphore
  meta.Duration = time.Since(start)
  return &rpcResponse{
    Version: "2.0",
    ID: call.ID,
    Result: result,
    Error: err,
    meta: meta,
  }
}

func (t *httpTransport) handleBatch(ctx context.Context, calls []rpcCall) []rpcResponse {
  results := make([]rpcResponse, len(calls))
  for i, call := range calls {
    results[i] = *t.handleSingle(ctx, &call)
  }
  return results
}
