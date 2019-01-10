// +build integration

package mtail

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/golang/glog"
	"github.com/google/mtail/internal/metrics"
	"github.com/google/mtail/internal/watcher"
)

const timeoutMultiplier = 3

// TestTempDir creates a temporary directory for use during tests. It returns
// the pathname, and a cleanup function.
func TestTempDir(t *testing.T) (string, func()) {
	t.Helper()
	name, err := ioutil.TempDir("", "mtail-test")
	if err != nil {
		t.Fatal(err)
	}
	return name, func() {
		if err := os.RemoveAll(name); err != nil {
			t.Fatalf("os.RemoveAll(%s): %s", name, err)
		}
	}
}

// TestMakeServer makes a new Server for use in tests, but does not start
// the server.  It returns the server, or any errors the new server creates.
func TestMakeServer(t *testing.T, pollInterval time.Duration, disableFsNotify bool, options ...func(*Server) error) (*Server, error) {
	t.Helper()
	w, err := watcher.NewLogWatcher(pollInterval, !disableFsNotify)
	if err != nil {
		t.Fatal(err)
	}

	return New(metrics.NewStore(), w, options...)
}

// TestStartServer creates a new Server and starts it running.  It
// returns the server, and a cleanup function.
func TestStartServer(t *testing.T, pollInterval time.Duration, disableFsNotify bool, options ...func(*Server) error) (*Server, func()) {
	t.Helper()
	options = append(options, BindAddress("", "0"))

	m, err := TestMakeServer(t, pollInterval, disableFsNotify, options...)
	if err != nil {
		t.Fatal(err)
	}

	errc := make(chan error, 1)
	go func() {
		err := m.Run()
		errc <- err
	}()

	glog.Infof("check that server is listening")
	count := 0
	for _, err := net.DialTimeout("tcp", m.Addr(), 10*time.Millisecond*timeoutMultiplier); err != nil && count < 10; count++ {
		glog.Infof("err: %s, retrying to dial %s", err, m.Addr())
		time.Sleep(100 * time.Millisecond * timeoutMultiplier)
	}
	if count >= 10 {
		t.Fatal("server wasn't listening after 10 attempts")
	}

	return m, func() {
		err := m.Close()
		if err != nil {
			t.Fatal(err)
		}
		select {
		case err = <-errc:
		case <-time.After(5 * time.Second):
			buf := make([]byte, 1<<16)
			n := runtime.Stack(buf, true)
			fmt.Fprintf(os.Stderr, "%s", buf[0:n])
			t.Fatal("timeout waiting for shutdown")
		}

		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestGetMetric fetches the expvar metrics from the Server at addr, and
// returns the value of one named name.  Callers are responsible for type
// assertions on the returned value.
func TestGetMetric(t *testing.T, addr, name string) interface{} {
	uri := fmt.Sprintf("http://%s/debug/vars", addr)
	client := &http.Client{
		Timeout: time.Duration(5 * time.Second),
	}
	resp, err := client.Get(uri)
	if err != nil {
		t.Fatal(err)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	var r map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &r); err != nil {
		t.Fatalf("%s: body was %s", err, buf.String())
	}
	return r[name]
}

// TestChdir changes current working directory, and returns a cleanup function
// to return to the previous directory.
func TestChdir(t *testing.T, dir string) func() {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	err = os.Chdir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return func() {
		err := os.Chdir(cwd)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// ExpectMetricDelta checks to see if the difference between a and b is want;
// it assumes both values are float64s that came from a TestGetMetric.
func ExpectMetricDelta(t *testing.T, a, b interface{}, want float64) {
	t.Helper()
	if a == nil {
		a = 0.
	}
	if b == nil {
		b = 0.
	}
	if a.(float64)-b.(float64) != want {
		t.Errorf("Unexpected delta: got %v - %v, want %g", a, b, want)
	}
}

// TestOpenFile creates a new file called name and returns the opened file.
func TestOpenFile(t *testing.T, name string) *os.File {
	t.Helper()
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// TestSetFlag sets the value of the commandline flag, and returns a cleanup function that restores the flag value.
func TestSetFlag(t *testing.T, name, value string) func() {
	t.Helper()
	val := flag.Lookup(name)

	flag.Set(name, value)
	flag.Parse()

	return func() {
		if val != nil {
			flag.Set(name, val.Value.String())
		}
	}
}