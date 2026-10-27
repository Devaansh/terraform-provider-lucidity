package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestListScalingPolicies(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(scalingPoliciesPath, func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, http.StatusOK, true, []map[string]any{{
			"id": 33, "name": "prod-db-volumes", "minBufferInGb": 30, "maxBufferInGb": 300, "idealUtilizationPercent": 80,
			"maxFsSizeInGb": 2000, "attachedInstanceCount": 4, "minDiskSkuSize": 8, "maxDiskSkuSize": 64,
		}}, nil, "r")
	})
	c, _ := newTenantTestClient(t, mux)

	got, err := c.ListScalingPolicies(context.Background())
	if err != nil || len(got) != 1 || got[0].ID != 33 || got[0].Name != "prod-db-volumes" || got[0].MaxDiskSkuSize != 64 || got[0].AttachedInstanceCount != 4 {
		t.Fatalf("got %+v, %v", got, err)
	}
	if p, ok := FindScalingPolicyByName(got, "prod-db-volumes"); !ok || p.ID != 33 {
		t.Fatalf("FindScalingPolicyByName: %+v %v", p, ok)
	}
}

// PUT resets omitted fields to 0, so every field must be sent, zeros too.
func TestUpdateScalingPolicy_SendsAllFields(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(scalingPoliciesPath+"/33", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("got method %s, want PUT", r.Method)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if len(body) != 7 || body["minDiskSkuSize"] != float64(0) {
			t.Fatalf("got body %v, want all 7 fields including zeros", body)
		}
		writeEnvelope(w, http.StatusOK, true, map[string]any{"id": 33, "name": "n"}, nil, "r")
	})
	c, _ := newTenantTestClient(t, mux)
	if _, err := c.UpdateScalingPolicy(context.Background(), 33, ScalingPolicySettings{Name: "n", MinBufferInGb: 1, IdealUtilizationPercent: 50, MaxFsSizeInGb: 10}); err != nil {
		t.Fatalf("UpdateScalingPolicy: %v", err)
	}
}

func TestAttachDetachScalingPolicy(t *testing.T) {
	var seen []string
	mux := http.NewServeMux()
	for _, action := range []string{"attach", "detach"} {
		action := action
		mux.HandleFunc(scalingPoliciesPath+"/33/"+action, func(w http.ResponseWriter, r *http.Request) {
			var body policyInstancesRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if r.Method != http.MethodPost || len(body.InstanceIdentifiers) != 1 || body.InstanceIdentifiers[0].ResourceGroup != "prod-rg" {
				t.Fatalf("%s: got %s %+v", action, r.Method, body)
			}
			seen = append(seen, action)
			writeEnvelope(w, http.StatusOK, true, nil, nil, "r")
		})
	}
	c, _ := newTenantTestClient(t, mux)
	inst := []PolicyInstanceIdentifier{{CloudProvider: "AZURE", CloudProviderAccountID: "sub", ResourceGroup: "prod-rg", InstanceID: "vm-db-01"}}
	if err := c.AttachScalingPolicy(context.Background(), 33, inst); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := c.DetachScalingPolicy(context.Background(), 33, inst); err != nil {
		t.Fatalf("detach: %v", err)
	}
	if len(seen) != 2 {
		t.Fatalf("got %v", seen)
	}
}

func TestGetScalingPolicy_NotFound(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(scalingPoliciesPath+"/90404", func(w http.ResponseWriter, r *http.Request) {
		writeEnvelope(w, http.StatusNotFound, false, nil, &envelopeError{Code: "NOT_FOUND", Message: "Scaling policy not found: 90404"}, "r")
	})
	c, _ := newTenantTestClient(t, mux)
	_, err := c.GetScalingPolicy(context.Background(), 90404)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "NOT_FOUND" {
		t.Fatalf("got %v", err)
	}
}

// Create isn't retried on 5xx (the caller looks the policy up by name).
func TestCreateScalingPolicy_DoesNotRetry5xx(t *testing.T) {
	var posts int
	mux := http.NewServeMux()
	mux.HandleFunc(scalingPoliciesPath, func(w http.ResponseWriter, r *http.Request) {
		posts++
		writeEnvelope(w, http.StatusInternalServerError, false, nil, &envelopeError{Code: "INTERNAL_ERROR", Message: "boom"}, "r")
	})
	c, _ := newTenantTestClient(t, mux)
	if _, err := c.CreateScalingPolicy(context.Background(), ScalingPolicySettings{Name: "n"}); err == nil || posts != 1 {
		t.Fatalf("got err %v after %d POSTs, want an error after exactly 1", err, posts)
	}
}
