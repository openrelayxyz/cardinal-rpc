package transports

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/inconshreveable/log15"
	"github.com/openrelayxyz/cardinal-rpc"
	"github.com/openrelayxyz/cardinal-types/metrics"
)

var (
	connectionCounter = metrics.NewMajorCounter("/rpc/ws/conn")
	pingPeriod = 29 * time.Second
)

type wsTransport struct {
	port      int64
	semaphore chan struct{}
	s         *http.Server
	running   bool
	registry  rpc.Registry
}

func NewWSTransport(port int64, semaphore chan struct{}, registry rpc.Registry) Transport {
	return &wsTransport{
		port:      port,
		semaphore: semaphore,
		registry:  registry,
	}
}

var upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin: func(*http.Request) bool { return true },
}

func (ws *wsTransport) handleWsFunc(w http.ResponseWriter, r *http.Request) {
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Info("upgrade error:", "error", err)
		return
	}
	connectionCounter.Inc(1)
	defer connectionCounter.Dec(1)
	defer c.Close()
	outputs := make(chan interface{}, 256)
	go func() {
		ticker := time.NewTicker(pingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				c.WriteMessage(websocket.PingMessage, []byte{})
			case output := <- outputs:
				switch v := output.(type) {
				case error:
					response, _ := json.Marshal(&rpc.Response{
						Version: "2.0",
						ID: json.RawMessage("-1"),
						Error: rpc.NewRPCError(-1, v.Error()),
					})
					c.WriteMessage(websocket.TextMessage, response)
				default:
					response, _ := json.Marshal(v)
					c.WriteMessage(websocket.TextMessage, response)
				}
			}
		}
	}()
	for {
		call := &rpc.Call{}
		_, body, err := c.ReadMessage()
		if err != nil {
			return
		}
		if err := json.Unmarshal(body, call); err == nil {
			response := ws.handleSingle(r.Context(), call, outputs)
			outputs <- response
			continue
		}
		calls := []rpc.Call{}
		if err := json.Unmarshal(body, &calls); err == nil {
			response := ws.handleBatch(r.Context(), calls, outputs)
			outputs <- response
		} else {
			outputs <- err
		}
	}
}

func (ws *wsTransport) Start(failure chan error) error {
	if ws.s != nil {
		return fmt.Errorf("wsTransport already started")
	}
	rand.Seed(time.Now().UnixNano())
	mux := http.NewServeMux()
	mux.HandleFunc("/", ws.handleWsFunc)
	ws.s = &http.Server{
		Addr:              fmt.Sprintf(":%v", ws.port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
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

func (ws *wsTransport) handleSingle(ctx context.Context, call *rpc.Call, outputs chan interface{}) *rpc.Response {
	ws.semaphore <- struct{}{}
	start := time.Now()
	result, err, meta := ws.registry.Call(ctx, call.Method, call.Params, outputs)
	<-ws.semaphore
	meta.Duration = time.Since(start)
	response := &rpc.Response{
		Version: "2.0",
		ID:      call.ID,
		Meta:    meta,
	}
	if err == nil {
		response.Result = result
	} else {
		response.Error = err
	}
	return response
}

func (ws *wsTransport) handleBatch(ctx context.Context, calls []rpc.Call, outputs chan interface{}) []rpc.Response {
	results := make([]rpc.Response, len(calls))
	for i, call := range calls {
		results[i] = *ws.handleSingle(ctx, &call, outputs)
	}
	return results
}
