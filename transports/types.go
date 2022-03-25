package transports

type hardEmpty struct{}

func (hardEmpty) MarshalJSON() ([]byte, error) {
  return []byte("null"), nil
}
