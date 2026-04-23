package openapi2jsonschema

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewWithOrigin_ResolvesHTTPExternalRefs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/schema/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"openapi": "3.1.0",
			"info": {"title": "Test API", "version": "1.0.0"},
			"paths": {
				"/responses": {"$ref": "paths/responses.json"}
			}
		}`))
	})
	mux.HandleFunc("/schema/paths/responses.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"post": {
				"requestBody": {
					"content": {
						"application/json": {
							"schema": {"$ref": "../components/schemas/CreateResponse.json"}
						}
					}
				},
				"responses": {
					"200": {
						"description": "ok",
						"content": {
							"application/json": {
								"schema": {"$ref": "../components/schemas/Response.json"}
							}
						}
					}
				}
			}
		}`))
	})
	mux.HandleFunc("/schema/components/schemas/CreateResponse.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"type": "object",
			"properties": {
				"input": {"type": "string"},
				"payload": {"$ref": "NestedPayload.json"}
			}
		}`))
	})
	mux.HandleFunc("/schema/components/schemas/Response.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"type": "object",
			"properties": {
				"id": {"type": "string"}
			}
		}`))
	})
	mux.HandleFunc("/schema/components/schemas/NestedPayload.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"type": "object",
			"properties": {
				"value": {"type": "integer"}
			}
		}`))
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	root := []byte(`{
		"openapi": "3.1.0",
		"info": {"title": "Test API", "version": "1.0.0"},
		"paths": {
			"/responses": {"$ref": "paths/responses.json"}
		}
	}`)

	conv, err := NewWithOrigin(root, srv.URL+"/schema/openapi.json")
	if err != nil {
		t.Fatalf("NewWithOrigin: %v", err)
	}

	data, err := conv.ExtractEndpointSchemas("/responses")
	if err != nil {
		t.Fatalf("ExtractEndpointSchemas: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	defs, ok := doc["$defs"].(map[string]any)
	if !ok {
		t.Fatalf("expected $defs in result")
	}
	if _, ok := defs["CreateResponse"]; !ok {
		t.Fatalf("expected CreateResponse in $defs")
	}
	if _, ok := defs["Response"]; !ok {
		t.Fatalf("expected Response in $defs")
	}
	if _, ok := defs["NestedPayload"]; !ok {
		t.Fatalf("expected NestedPayload in $defs")
	}
}
