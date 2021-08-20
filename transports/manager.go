package transports

import (
  "github.com/openrelayxyz/cardinal-rpc"
  "os"
  "os/signal"
  "syscall"
)

type TransportManager struct{
  transports []Transport
  semaphore  chan struct{}
  registry   rpc.Registry
}

func NewTransportManager(concurrency int) *TransportManager {
  return &TransportManager{
    transports: []Transport{},
    semaphore:  make(chan struct{}, concurrency),
    registry: rpc.NewRegistry(),
  }
}

func (tm *TransportManager) Register(namespace string, service interface{}) {
  tm.registry.Register(namespace, service)
}

func (tm *TransportManager) AddHTTPServer(port int64) {
  tm.transports = append(tm.transports, NewHTTPTransport(port, tm.semaphore, tm.registry))
}

func (tm *TransportManager) Run() error {
  failure := make(chan error)
  for _, t := range tm.transports {
    t.Start(failure)
  }
  sigs := make(chan os.Signal, 1)
  signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
  select {
  case err := <- failure:
    return err
  case <-sigs:
    return nil
  }
}

func (tm *TransportManager) Stop() {
  for _, t := range tm.transports {
    t.Stop()
  }
}
