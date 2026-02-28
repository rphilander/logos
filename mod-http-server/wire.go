package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

func ReadMsg(r io.Reader) (json.RawMessage, error) {
	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return nil, fmt.Errorf("read length: %w", err)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return json.RawMessage(buf), nil
}

func WriteMsg(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := binary.Write(w, binary.BigEndian, uint32(len(data))); err != nil {
		return fmt.Errorf("write length: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	return nil
}
