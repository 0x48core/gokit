package httpserver

import (
	"context"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type TestServerSuite struct {
	suite.Suite
}

func TestTestServerSuite(t *testing.T) {
	suite.Run(t, new(TestServerSuite))
}

func (suite *TestServerSuite) listen() net.Listener {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	suite.Require().NoError(err)
	return ln
}

func (suite *TestServerSuite) Test_New_Defaults() {
	// Action
	s := New(nil, http.NotFoundHandler())

	// Assert
	suite.Equal(defaultAddr, s.srv.Addr)
	suite.Equal(defaultReadTimeout, s.srv.ReadTimeout)
	suite.Equal(defaultReadHeaderTimeout, s.srv.ReadHeaderTimeout)
	suite.Equal(defaultWriteTimeout, s.srv.WriteTimeout)
	suite.Equal(defaultIdleTimeout, s.srv.IdleTimeout)
	suite.Equal(defaultShutdownTimeout, s.shutdownTimeout)
}

func (suite *TestServerSuite) Test_New_Config() {
	// Action
	s := New(&Config{Addr: ":9090", WriteTimeout: time.Second}, http.NotFoundHandler())

	// Assert
	suite.Equal(":9090", s.srv.Addr)
	suite.Equal(time.Second, s.srv.WriteTimeout)
	suite.Equal(defaultReadTimeout, s.srv.ReadTimeout)
}

func (suite *TestServerSuite) Test_Serve_GracefulShutdown() {
	// Arrange
	started := make(chan struct{})
	release := make(chan struct{})
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte("done"))
	})
	ln := suite.listen()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() { served <- New(nil, handler).Serve(ctx, ln) }()

	body := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err != nil {
			body <- err.Error()
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		body <- string(b)
	}()
	<-started

	// Action
	cancel()
	time.Sleep(50 * time.Millisecond)
	close(release)

	// Assert
	suite.Equal("done", <-body)
	suite.NoError(<-served)
}

func (suite *TestServerSuite) Test_Serve_ShutdownTimeout() {
	// Arrange
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	handler := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
	})
	ln := suite.listen()
	ctx, cancel := context.WithCancel(context.Background())
	served := make(chan error, 1)
	go func() {
		served <- New(&Config{ShutdownTimeout: 50 * time.Millisecond}, handler).Serve(ctx, ln)
	}()
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-started

	// Action
	cancel()

	// Assert
	suite.ErrorIs(<-served, context.DeadlineExceeded)
}

func (suite *TestServerSuite) Test_Run_ListenError() {
	// Arrange
	ln := suite.listen()
	defer ln.Close()

	// Action
	err := New(&Config{Addr: ln.Addr().String()}, http.NotFoundHandler()).Run(context.Background())

	// Assert
	suite.ErrorContains(err, "httpserver: listen")
}

func (suite *TestServerSuite) Test_Serve_ListenerClosed() {
	// Arrange
	ln := suite.listen()
	suite.Require().NoError(ln.Close())

	// Action
	err := New(nil, http.NotFoundHandler()).Serve(context.Background(), ln)

	// Assert
	suite.ErrorContains(err, "httpserver: serve")
}
