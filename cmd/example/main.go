package main

import (
  "github.com/openrelayxyz/cardinal-rpc/transports"
)

func main() {
  tm := transports.NewTransportManager(32)

  tm.AddHTTPServer(8000)
  tm.AddWSServer(8080)
  tm.Register("test", &Service{})
  tm.Run(9999)



}
 type Service struct {
 }

func (s *Service) Hello() string {
  return "goodbuy horses"
}

func (s *Service) Macaroni() string {
  return "is easy to make"
}

func (s *Service) Listen() string {
  return "to Miles Davis"
}
