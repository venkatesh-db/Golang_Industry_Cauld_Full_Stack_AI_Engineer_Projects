package queue

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// Redis is the small command subset this service needs. It deliberately keeps
// Redis behind an interface so it can be replaced by a tested client library.
type Redis interface {
	Do(command string, args ...string) (any, error)
}

type RedisClient struct {
	addr    string
	timeout time.Duration
}

func NewRedisClient(addr string) *RedisClient {
	return &RedisClient{addr: addr, timeout: 3 * time.Second}
}

func (c *RedisClient) Do(command string, args ...string) (any, error) {
	conn, err := net.DialTimeout("tcp", c.addr, c.timeout)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(c.timeout)); err != nil {
		return nil, err
	}
	parts := append([]string{command}, args...)
	if _, err := fmt.Fprintf(conn, "*%d\r\n", len(parts)); err != nil {
		return nil, err
	}
	for _, part := range parts {
		if _, err := fmt.Fprintf(conn, "$%d\r\n%s\r\n", len(part), part); err != nil {
			return nil, err
		}
	}
	return readRESP(bufio.NewReader(conn))
}

func readRESP(reader *bufio.Reader) (any, error) {
	prefix, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	switch prefix {
	case '+':
		return line, nil
	case '-':
		return nil, errors.New(line)
	case ':':
		return strconv.ParseInt(line, 10, 64)
	case '$':
		length, err := strconv.Atoi(line)
		if err != nil || length < -1 {
			return nil, fmt.Errorf("invalid bulk reply length %q", line)
		}
		if length == -1 {
			return nil, nil
		}
		data := make([]byte, length+2)
		if _, err := io.ReadFull(reader, data); err != nil {
			return nil, err
		}
		return string(data[:length]), nil
	case '*':
		length, err := strconv.Atoi(line)
		if err != nil || length < -1 {
			return nil, fmt.Errorf("invalid array reply length %q", line)
		}
		if length == -1 {
			return nil, nil
		}
		array := make([]any, length)
		for i := range array {
			if array[i], err = readRESP(reader); err != nil {
				return nil, err
			}
		}
		return array, nil
	default:
		return nil, fmt.Errorf("unsupported redis reply prefix %q", prefix)
	}
}
