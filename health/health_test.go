package health

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type TestHealthSuite struct {
	suite.Suite
	logs       *bytes.Buffer
	oldDefault *slog.Logger
}

func TestTestHealthSuite(t *testing.T) {
	suite.Run(t, new(TestHealthSuite))
}

func (suite *TestHealthSuite) SetupTest() {
	suite.logs = &bytes.Buffer{}
	suite.oldDefault = slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(suite.logs, nil)))
}

func (suite *TestHealthSuite) TearDownTest() {
	slog.SetDefault(suite.oldDefault)
}

func (suite *TestHealthSuite) serve(h http.Handler) (int, Result) {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	suite.Equal("application/json", rec.Header().Get("Content-Type"))
	var res Result
	suite.Require().NoError(json.Unmarshal(rec.Body.Bytes(), &res))
	return rec.Code, res
}

func pass(context.Context) error { return nil }

func (suite *TestHealthSuite) Test_Liveness_IgnoresChecks() {
	// Arrange
	c := New(WithCheck("db", func(context.Context) error { return errors.New("down") }))

	// Action
	code, res := suite.serve(c.Liveness())

	// Assert
	suite.Equal(http.StatusOK, code)
	suite.Equal(StatusOK, res.Status)
}

func (suite *TestHealthSuite) Test_Readiness_AllPass() {
	// Arrange
	c := New(WithCheck("db", pass), WithCheck("redis", pass))

	// Action
	code, res := suite.serve(c.Readiness())

	// Assert
	suite.Equal(http.StatusOK, code)
	suite.Equal(StatusOK, res.Status)
	suite.Equal(StatusOK, res.Checks["db"].Status)
	suite.Equal(StatusOK, res.Checks["redis"].Status)
}

func (suite *TestHealthSuite) Test_Readiness_NoChecks() {
	// Action
	code, res := suite.serve(New().Readiness())

	// Assert
	suite.Equal(http.StatusOK, code)
	suite.Equal(StatusOK, res.Status)
}

func (suite *TestHealthSuite) Test_Readiness_OneFails() {
	// Arrange
	c := New(
		WithCheck("db", pass),
		WithCheck("redis", func(context.Context) error { return errors.New("connection refused") }))

	// Action
	code, res := suite.serve(c.Readiness())

	// Assert
	suite.Equal(http.StatusServiceUnavailable, code)
	suite.Equal(StatusFail, res.Status)
	suite.Equal(StatusOK, res.Checks["db"].Status)
	suite.Equal(StatusFail, res.Checks["redis"].Status)
	suite.Equal("connection refused", res.Checks["redis"].Error)
}

func (suite *TestHealthSuite) Test_Check_Timeout() {
	// Arrange
	release := make(chan struct{})
	defer close(release)
	c := New(
		WithTimeout(20*time.Millisecond),
		WithCheck("respects ctx", func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}),
		WithCheck("ignores ctx", func(context.Context) error {
			<-release
			return nil
		}))

	// Action
	start := time.Now()
	res := c.Check(context.Background())

	// Assert
	suite.Less(time.Since(start), time.Second)
	suite.Equal(StatusFail, res.Status)
	suite.Equal(context.DeadlineExceeded.Error(), res.Checks["respects ctx"].Error)
	suite.Equal(context.DeadlineExceeded.Error(), res.Checks["ignores ctx"].Error)
}

func (suite *TestHealthSuite) Test_Check_RunsConcurrently() {
	// Arrange
	slow := func(context.Context) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	}
	c := New(WithCheck("a", slow), WithCheck("b", slow), WithCheck("c", slow))

	// Action
	start := time.Now()
	res := c.Check(context.Background())

	// Assert
	suite.Equal(StatusOK, res.Status)
	suite.Less(time.Since(start), 250*time.Millisecond)
}

func (suite *TestHealthSuite) Test_Check_Panic() {
	// Arrange
	c := New(WithCheck("db", func(context.Context) error { panic("nil pool") }))

	// Action
	res := c.Check(context.Background())

	// Assert
	suite.Equal(StatusFail, res.Status)
	suite.Equal("panic: nil pool", res.Checks["db"].Error)
}

func (suite *TestHealthSuite) Test_Register() {
	// Arrange
	c := New()
	c.Register("db", func(context.Context) error { return errors.New("down") })

	// Action
	code, res := suite.serve(c.Readiness())

	// Assert
	suite.Equal(http.StatusServiceUnavailable, code)
	suite.Equal(StatusFail, res.Checks["db"].Status)
}

func (suite *TestHealthSuite) Test_Drain() {
	// Arrange
	called := false
	c := New(WithCheck("db", func(context.Context) error {
		called = true
		return nil
	}))

	// Action
	c.Drain()
	code, res := suite.serve(c.Readiness())

	// Assert
	suite.Equal(http.StatusServiceUnavailable, code)
	suite.Equal(StatusFail, res.Status)
	suite.False(called)
	liveCode, _ := suite.serve(c.Liveness())
	suite.Equal(http.StatusOK, liveCode)
}

func (suite *TestHealthSuite) Test_LogsTransitionsOnly() {
	// Arrange
	var failing bool
	c := New(WithCheck("db", func(context.Context) error {
		if failing {
			return errors.New("down")
		}
		return nil
	}))

	// Action
	c.Check(context.Background())
	failing = true
	c.Check(context.Background())
	c.Check(context.Background())
	failing = false
	c.Check(context.Background())
	c.Check(context.Background())

	// Assert
	var msgs []string
	for line := range strings.Lines(suite.logs.String()) {
		m := map[string]any{}
		suite.Require().NoError(json.Unmarshal([]byte(line), &m))
		msgs = append(msgs, m["level"].(string)+" "+m["msg"].(string))
	}
	suite.Equal([]string{"WARN health check failing", "INFO health check recovered"}, msgs)
}
