package transports


import (
	"flag"
	"fmt"
	"math/rand"
	"time"
	"context"
	"net/http"
	"encoding/json"
	// "io/ioutil"

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

var addr = flag.String("addr", "localhost:8080", "http service address")

var upgrader = websocket.Upgrader{} // use default options

func (ws *wsTransport) echo(w http.ResponseWriter, r *http.Request) {
	fmt.Println("we out chear")
	log.Info("inside of echo", nil, nil)
	c, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Info("upgrade:", err, nil)
		return
	}
	defer c.Close()
	// for {
	// 	body, err := ioutil.ReadAll(r.Body)
	// 	fmt.Println(len(body))
	// 	log.Info("body is", body, nil)
	//   if err != nil { log.Info("error on line 49", err, nil) }
	// 	mt, message, err := c.ReadMessage()
	// 	call := &rpc.Call{}
	// 	json.Unmarshal(body, call)
	// 	fmt.Println(mt, call, err)
	// 	if err != nil {
	// 		log.Info("read:", err, nil)
	// 		break
	// 	}
	// 	log.Info("recv:", mt, message, err, nil)
	// 	err = c.WriteMessage(mt, message)
	// 	if err != nil {
	// 		log.Info("write:", err, nil)
	// 		break
	// 	}
	// }
	for {
	call := &rpc.Call{}
  mt, body, err := c.ReadMessage()
	log.Info("body is:",body, nil)
  if err != nil { log.Info("there is an error on line 64", err, nil) }
  if err := json.Unmarshal(body, call); err == nil {
    response := ws.handleSingle(r.Context(), call)
    writeResponse(w, response)
		fmt.Println(r.Body)
		fmt.Println(call.Method)
		log.Info("call is", call)
    // return
  } else {
		log.Info("there is an errror on line 71", err, nil)
		fmt.Println(err)
	}
	err = c.WriteMessage(mt, []byte(ws.handleSingle(r.Context(), call).Result.(string)))
  calls := []rpc.Call{}
  if err := json.Unmarshal(body, &calls); err == nil {
    response := ws.handleBatch(r.Context(), calls)
    writeResponse(w, response)
		log.Info("call list is", calls)
		return

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

// func home(w http.ResponseWriter, r *http.Request) {
// 	homeTemplate.Execute(w, "ws://"+r.Host+"/echo")
// }

// func main() {
// 	flag.Parse()
// 	log.SetFlags(0)
// 	http.HandleFunc("/echo", echo)
// 	http.HandleFunc("/", home)
// 	log.Fatal(http.ListenAndServe(*addr, nil))
// }
