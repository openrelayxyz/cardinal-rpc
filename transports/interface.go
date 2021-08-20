package transports

type Transport interface{
  Start(chan error) error
  Stop() error
}
