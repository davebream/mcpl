package daemon

import (
	"bufio"
	"encoding/json"
	"net"
	"sync"

	"github.com/google/uuid"
)

type Session struct {
	ID         string
	Conn       net.Conn
	ServerName string
	scanner    *bufio.Scanner
	mu         sync.Mutex
}

func NewSession(conn net.Conn, serverName string) *Session {
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 4096), 10*1024*1024) // 10MB max message size
	return &Session{
		ID:         uuid.New().String(),
		Conn:       conn,
		ServerName: serverName,
		scanner:    scanner,
	}
}

func (s *Session) ReadLine() ([]byte, error) {
	if s.scanner.Scan() {
		// Return a copy — scanner.Bytes() is invalidated on next Scan()
		line := s.scanner.Bytes()
		result := make([]byte, len(line))
		copy(result, line)
		return result, nil
	}
	if err := s.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, net.ErrClosed
}

func (s *Session) WriteLine(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	line := make([]byte, len(data)+1)
	copy(line, data)
	line[len(data)] = '\n'
	_, err := s.Conn.Write(line)
	return err
}

func (s *Session) WriteJSON(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.WriteLine(data)
}

func (s *Session) Close() error {
	return s.Conn.Close()
}
