# Cardinal RPC

Cardinal RPC is a framework for creating Web3 compatible RPC servers, built for
the Cardinal Blockchain Paraclient.

It uses reflection to convert Go structs into JSON-RPC methods, allowing
developers to write Go code without putting much effort on serialization,
deserialization, and other HTTP handling concerns.

It currently supports an HTTP transport, with Websockets coming in the future.

## Usage

The easiest way to get started is with the transport manager:


```
import (
  "github.com/openrelayxyz/cardinal-rpc/transports"
)

func main() {
  tm := transports.NewTransportManager(concurrencyLevel)
  tm.Register("namespace", &myService{})
  tm.AddHTTPServer(8000)
  if err := tm.Run(); err != nil { panic(err.Error()) }
}
```

The transport manager specifies a concurrencyLevel, which is the number of
goroutines which can concurrently execute RPC calls. 
