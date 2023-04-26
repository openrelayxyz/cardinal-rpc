package transports

import (
  "context"
  "encoding/json"
  "fmt"
  "github.com/openrelayxyz/cardinal-rpc"
  "os"
  "os/signal"
  "syscall"
  "net/http"
  "time"
  log "github.com/inconshreveable/log15"
)


type TransportManager struct{
  transports   []Transport
  semaphore    chan struct{}
  registry     rpc.Registry
  healthChecks []rpc.HealthCheck
  s            *http.Server
  shutdown     bool
}

func NewTransportManager(concurrency int) *TransportManager {
  return &TransportManager{
    transports: []Transport{},
    registry: rpc.NewRegistry(concurrency),
    healthChecks: []rpc.HealthCheck{},
    shutdown: false,
  }
}

func (tm *TransportManager) Register(namespace string, service interface{}) {
  tm.registry.Register(namespace, service)
}

func (tm *TransportManager) RegisterMiddleware(item rpc.Middleware) {
  tm.registry.RegisterMiddleware(item)
}

func (tm *TransportManager) RegisterHeightFeed(ch <-chan int64) {
	tm.registry.RegisterHeightFeed(ch)
}

func (tm *TransportManager) RegisterHealthCheck(hc rpc.HealthCheck) {
  tm.healthChecks = append(tm.healthChecks, hc)
}

func (tm *TransportManager) SetBlockWaitDuration(d time.Duration) {
	tm.registry.SetBlockWaitDuration(d)
}

func (tm *TransportManager) OnMissing(fn func(*rpc.CallContext, string, []json.RawMessage) (interface{}, *rpc.RPCError, *rpc.CallMetadata)) {
  tm.registry.OnMissing(fn)
}

func (tm *TransportManager) AddHTTPServer(port int64) {
  tm.transports = append(tm.transports, NewHTTPTransport(port, tm.registry))
}

func (tm *TransportManager) AddWSServer(port int64) {
  tm.transports = append(tm.transports, NewWSTransport(port, tm.registry))
}

func (tm *TransportManager) handleHealthCheck(w http.ResponseWriter, r *http.Request) {
  w.Header().Set("Content-Type", "application/json")
  hasWarning := false
  if tm.shutdown {
    w.WriteHeader(500)
    w.Write([]byte(`{"ok": false}\n`))
    return
  }
  for _, hc := range tm.healthChecks {
    status := hc.Healthy()
    if status == rpc.Unavailable {
      w.WriteHeader(500)
      w.Write([]byte(`{"ok": false}\n`))
      return
    }
    if status == rpc.Warning {
      hasWarning = true
    }
  }
  if hasWarning {
    w.WriteHeader(429)
    w.Write([]byte(`{"ok": false}\n`))
    return
  }
  w.WriteHeader(200)
  w.Write([]byte(`{"ok": true}\n`))
}

func (tm *TransportManager) Run(hcport int64) error {
  failure := make(chan error)
  for _, t := range tm.transports {
    t.Start(failure)
  }
  if hcport > 0 {
    mux := http.NewServeMux()
    mux.HandleFunc("/", tm.handleHealthCheck)
    s := &http.Server{
      Addr: fmt.Sprintf(":%v", hcport),
      Handler: mux,
      ReadHeaderTimeout: 250 * time.Millisecond,
      MaxHeaderBytes: 1 << 20,
    }
    go func() { failure <- s.ListenAndServe() }()
    defer s.Shutdown(context.Background())
  }

  sigs := make(chan os.Signal, 1)
  signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
  select {
  case err := <- failure:
    return err
  case <-sigs:
    log.Info("Caught shutdown signal. Waiting 30s ")
    tm.shutdown = true
    time.Sleep(30 * time.Second)
    return nil
  }
}

func (tm *TransportManager) Stop() {
  for _, t := range tm.transports {
    t.Stop()
  }
}

func (tm *TransportManager) Caller() rpc.RegistryCallable {
  return tm.registry
}
