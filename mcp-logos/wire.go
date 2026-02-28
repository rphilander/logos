package main

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync/atomic"
)

var msgCounter uint64

func nextID() string {
	n := atomic.AddUint64(&msgCounter, 1)
	return fmt.Sprintf("r%d", n)
}

func writeMsg(conn net.Conn, msg map[string]any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	length := uint32(len(data))
	if err := binary.Write(conn, binary.BigEndian, length); err != nil {
		return fmt.Errorf("write length: %w", err)
	}
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("write body: %w", err)
	}
	return nil
}

func readMsg(conn net.Conn) (map[string]any, error) {
	var length uint32
	if err := binary.Read(conn, binary.BigEndian, &length); err != nil {
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("read length: %w", err)
	}
	data := make([]byte, length)
	if _, err := io.ReadFull(conn, data); err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	return msg, nil
}
