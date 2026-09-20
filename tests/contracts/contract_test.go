package contracts

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

func TestOpenAPIContractHasRequiredOperations(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "..", "..", "api", "openapi", "openapi.yaml")
	doc, err := openapi3.NewLoader().LoadFromFile(path)
	if err != nil {
		t.Fatalf("load OpenAPI contract: %v", err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate OpenAPI contract: %v", err)
	}

	required := map[string]string{
		"POST /api/v1/auth/login":                      "login",
		"POST /api/v1/auth/logout":                     "logout",
		"GET /api/v1/auth/status":                      "authentication status",
		"GET /api/v1/auth/csrf":                        "csrf rotation",
		"GET /api/v1/me":                               "current principal",
		"GET /api/v1/system/health":                    "system health",
		"GET /api/v1/nodes":                            "list nodes",
		"POST /api/v1/nodes":                           "create node",
		"GET /api/v1/nodes/{id}":                       "node detail",
		"GET /api/v1/instances":                        "list instances",
		"POST /api/v1/instances":                       "create instance",
		"GET /api/v1/instances/{id}":                   "instance detail",
		"PATCH /api/v1/instances/{id}":                 "update instance",
		"POST /api/v1/instances/{id}/actions/{action}": "instance action",
		"GET /api/v1/jobs":                             "list jobs",
		"GET /api/v1/jobs/{id}":                        "job status",
		"GET /api/v1/audit":                            "audit records",
		"GET /api/v1/users":                            "list users",
		"POST /api/v1/users":                           "create user",
		"PATCH /api/v1/users/{id}":                    "update user",
		"GET /api/v1/ws":                               "realtime events",
	}
	for route, description := range required {
		method, routePath := splitRoute(route)
		item := doc.Paths.Find(routePath)
		if item == nil {
			t.Errorf("missing %s route %s", description, route)
			continue
		}
		op := item.GetOperation(method)
		if op == nil {
			t.Errorf("missing %s operation %s", description, route)
			continue
		}
		if op.Responses == nil || op.Responses.Len() == 0 {
			t.Errorf("operation %s has no response schema", route)
		}
	}
}

func splitRoute(route string) (string, string) {
	for index, character := range route {
		if character == ' ' {
			return route[:index], route[index+1:]
		}
	}
	return "", route
}
