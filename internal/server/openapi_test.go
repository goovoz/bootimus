package server

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"bootimus/internal/admin"
)

func registeredRoutes(t *testing.T) map[string]bool {
	t.Helper()
	src, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	routes := map[string]bool{}
	for _, m := range regexp.MustCompile(`mux\.Handle(?:Func)?\("([^"]+)"`).FindAllStringSubmatch(string(src), -1) {
		routes[m[1]] = true
	}
	return routes
}

func specPathRegistered(path string, routes map[string]bool) bool {
	if routes[path] {
		return true
	}
	prefix := path
	if i := strings.Index(prefix, "{"); i >= 0 {
		prefix = prefix[:i]
	}
	for route := range routes {
		if route != "/" && strings.HasSuffix(route, "/") && strings.HasPrefix(prefix, route) {
			return true
		}
	}
	return false
}

func TestOpenAPIMatchesRegisteredRoutes(t *testing.T) {
	routes := registeredRoutes(t)
	doc := admin.BuildOpenAPI("test", "")
	for path := range doc.Paths {
		if !specPathRegistered(path, routes) {
			t.Errorf("OpenAPI path %s is not registered in server.go", path)
		}
	}
	for route := range routes {
		if !strings.HasPrefix(route, "/api/") {
			continue
		}
		if _, ok := doc.Paths[route]; !ok {
			t.Errorf("route %s is registered in server.go but missing from APISections in internal/admin/openapi.go", route)
		}
	}
}

func TestOpenAPIAccessFlagsMatchRouteLists(t *testing.T) {
	doc := admin.BuildOpenAPI("test", "")
	for path, ops := range doc.Paths {
		if !strings.HasPrefix(path, "/api/") {
			continue
		}
		for method, op := range ops {
			if op.Public != publicRoutes[path] {
				t.Errorf("%s %s: x-bootimus-public=%v but publicRoutes says %v", method, path, op.Public, publicRoutes[path])
			}
			if method != "get" {
				continue
			}
			want := viewerReadRoutes[path] || publicRoutes[path]
			if op.ReadOnly != want {
				t.Errorf("GET %s: x-bootimus-readonly=%v but the read-only route list says %v", path, op.ReadOnly, want)
			}
		}
	}
}

func TestOpenAPIEndpointServesDocument(t *testing.T) {
	env := newAccessEnv(t)
	code, body := env.do(http.MethodGet, "/api/openapi.json", "")
	if code != http.StatusOK {
		t.Fatalf("GET /api/openapi.json: want 200, got %d %s", code, body)
	}
	var doc admin.OpenAPIDocument
	if err := json.Unmarshal([]byte(body), &doc); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if doc.OpenAPI != "3.0.3" || len(doc.Paths) == 0 {
		t.Fatalf("unexpected document: openapi=%q paths=%d", doc.OpenAPI, len(doc.Paths))
	}
	if len(doc.Servers) != 1 || !strings.HasPrefix(doc.Servers[0].URL, "http://") {
		t.Errorf("servers should be derived from the request host, got %+v", doc.Servers)
	}
	ids := map[string]bool{}
	for path, ops := range doc.Paths {
		for method, op := range ops {
			if ids[op.OperationID] {
				t.Errorf("duplicate operationId %s at %s %s", op.OperationID, method, path)
			}
			ids[op.OperationID] = true
		}
	}
}
