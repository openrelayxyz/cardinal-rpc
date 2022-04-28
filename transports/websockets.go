package transports


import (
	"fmt"
	"math/rand"
	"time"
	"context"
	"net/http"
	"encoding/json"

	"github.com/openrelayxyz/cardinal-rpc"
	"github.com/gorilla/websocket"
	log "github.com/inconshreveable/log15"
)

type wsTransport struct {
  port int64
  semaphore chan struct{}
  s *http.Server
  running bool
  registry rpc.Registry
}

func NewWSTransport(port int64, semaphore chan struct{}, registry rpc.Registry) Transport {
  return &wsTransport{
    port: port,
    semaphore: semaphore,
    registry: registry,
  }
}

var upgrader = websocket.Upgrader{} // use default options

func (ws *wsTransport) echo(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Info("upgrade error:", "error", err)
		return
	}
	defer c.Close()
	for {
	call := &rpc.Call{}
  mt, body, err := c.ReadMessage()
  if err != nil { return }
  if err := json.Unmarshal(body, call); err == nil {
    response := ws.handleSingle(r.Context(), call)
		r, _ := json.Marshal(response)
		c.WriteMessage(mt, r)
		continue
  }
  calls := []rpc.Call{}
  if err := json.Unmarshal(body, &calls); err == nil {
    response := ws.handleBatch(r.Context(), calls)
		for _, item := range response {
			r, _ := json.Marshal(item)
			c.WriteMessage(mt, r)
		}
  } else {
    handleError(w, err)
  }
}
}

func (ws *wsTransport) Start(failure chan error) error {
  if ws.s != nil {
    return fmt.Errorf("wsTransport already started")
  }
  rand.Seed(time.Now().UnixNano())
  mux := http.NewServeMux()
  mux.HandleFunc("/", ws.echo)
  ws.s = &http.Server{
    Addr: fmt.Sprintf(":%v", ws.port),
    Handler: mux,
    ReadHeaderTimeout: 5 * time.Second,
    IdleTimeout: 120 * time.Second,
    MaxHeaderBytes: 1 << 20,
  }
  go func() { failure <- ws.s.ListenAndServe() }()
  log.Info("Running web socket transport", "port:", ws.port)
  ws.running = true
  return nil
}

func (ws *wsTransport) Stop() error {
	ws.running = false
	return ws.s.Shutdown(context.Background())
}

func (ws *wsTransport) handleSingle(ctx context.Context, call *rpc.Call) *rpc.Response {
  ws.semaphore <- struct{}{}
  start := time.Now()
  result, err, meta := ws.registry.Call(ctx, call.Method, call.Params)
  <-ws.semaphore
  meta.Duration = time.Since(start)
  response := &rpc.Response{
    Version: "2.0",
    ID: call.ID,
    Meta: meta,
  }
  if err == nil {
    response.Result = result
  } else {
    response.Error = err
  }
  return response
}

func (ws *wsTransport) handleBatch(ctx context.Context, calls []rpc.Call) []rpc.Response {
  results := make([]rpc.Response, len(calls))
  for i, call := range calls {
    results[i] = *ws.handleSingle(ctx, &call)
  }
  return results
}
