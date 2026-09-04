package singbox

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func TestExternalIPQueryUsesSOCKS5AndBoundsResponse(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer connection.Close()
		greeting := make([]byte, 3)
		if _, readErr := io.ReadFull(connection, greeting); readErr != nil {
			done <- readErr
			return
		}
		if _, writeErr := connection.Write([]byte{5, 0}); writeErr != nil {
			done <- writeErr
			return
		}
		header := make([]byte, 4)
		if _, readErr := io.ReadFull(connection, header); readErr != nil {
			done <- readErr
			return
		}
		addressLength := 4
		if header[3] == 3 {
			length := make([]byte, 1)
			if _, readErr := io.ReadFull(connection, length); readErr != nil {
				done <- readErr
				return
			}
			addressLength = int(length[0])
		} else if header[3] == 4 {
			addressLength = 16
		}
		if _, readErr := io.CopyN(io.Discard, connection, int64(addressLength+2)); readErr != nil {
			done <- readErr
			return
		}
		if _, writeErr := connection.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 80}); writeErr != nil {
			done <- writeErr
			return
		}
		reader := bufio.NewReader(connection)
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				done <- readErr
				return
			}
			if line == "\r\n" {
				break
			}
		}
		body := "203.0.113.44"
		_, writeErr := fmt.Fprintf(connection, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", len(body), body)
		done <- writeErr
	}()
	address, err := queryExternalIP(context.Background(), listener.Addr().String(), "http://198.51.100.10/", 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if address != "203.0.113.44" {
		t.Fatalf("external IP = %q", address)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSOCKS5DialRejectsUnsupportedNetworkWithoutDialing(t *testing.T) {
	t.Parallel()
	_, err := socks5DialContext(context.Background(), "127.0.0.1:1", "udp", "example.com:53", time.Second)
	if err == nil || !strings.Contains(err.Error(), "TCP only") {
		t.Fatalf("error = %v", err)
	}
}
