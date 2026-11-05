package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/luciditycloud/terraform-provider-lucidity/internal/client"
)

// mockPolicyServer is a stateful stand-in for the Scaling Policies API. Like
// the real create, its POST response reports the disk SKU sizes and the
// attached count as 0.
type mockPolicyServer struct {
	mu       sync.Mutex
	policies map[int64]*client.ScalingPolicy
	nextID   int64
	attached map[string]bool  // "<policyID>/<instanceID>"
	policyOf map[string]int64 // instanceID -> attached policy, as the instances API reports it
	detaches int
}

func newMockPolicyServer(t *testing.T) (*client.Client, *mockPolicyServer) {
	t.Helper()
	m := &mockPolicyServer{policies: map[int64]*client.ScalingPolicy{}, nextID: 33, attached: map[string]bool{}, policyOf: map[string]int64{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/oauth2/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"accessToken": "mock-access-token"})
	})
	mux.HandleFunc("/autoscaler/v1/scaling-policies", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		switch r.Method {
		case http.MethodGet:
			list := []client.ScalingPolicy{}
			for _, p := range m.policies {
				list = append(list, *p)
			}
			writeMockEnvelope(w, http.StatusOK, true, list, "", "")
		case http.MethodPost:
			var s client.ScalingPolicySettings
			_ = json.NewDecoder(r.Body).Decode(&s)
			for _, p := range m.policies {
				if p.Name == s.Name {
					writeMockEnvelope(w, http.StatusConflict, false, nil, "CONFLICT", "A scaling policy with this name already exists")
					return
				}
			}
			p := &client.ScalingPolicy{ID: m.nextID, ScalingPolicySettings: s}
			m.policies[p.ID] = p
			m.nextID++
			echo := *p
			echo.MinDiskSkuSize, echo.MaxDiskSkuSize = 0, 0
			writeMockEnvelope(w, http.StatusCreated, true, echo, "", "")
		}
	})
	mux.HandleFunc("/autoscaler/v1/scaling-policies/", func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, "/autoscaler/v1/scaling-policies/")
		idStr, action, _ := strings.Cut(rest, "/")
		id, _ := strconv.ParseInt(idStr, 10, 64)
		m.mu.Lock()
		defer m.mu.Unlock()
		p, ok := m.policies[id]
		if !ok {
			writeMockEnvelope(w, http.StatusNotFound, false, nil, "NOT_FOUND", "Scaling policy not found: "+idStr)
			return
		}
		switch {
		case action == "" && r.Method == http.MethodGet:
			writeMockEnvelope(w, http.StatusOK, true, p, "", "")
		case action == "" && r.Method == http.MethodPut:
			var s client.ScalingPolicySettings
			_ = json.NewDecoder(r.Body).Decode(&s)
			p.ScalingPolicySettings = s
			writeMockEnvelope(w, http.StatusOK, true, p, "", "")
		case action == "" && r.Method == http.MethodDelete:
			delete(m.policies, id)
			writeMockEnvelope(w, http.StatusOK, true, nil, "", "")
		case action == "attach" || action == "detach":
			var body struct {
				InstanceIdentifiers []client.PolicyInstanceIdentifier `json:"instanceIdentifiers"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			for _, i := range body.InstanceIdentifiers {
				key := idStr + "/" + i.InstanceID
				if action == "attach" {
					m.attached[key] = true
					m.policyOf[i.InstanceID] = id
					p.AttachedInstanceCount++
				} else {
					delete(m.attached, key)
					delete(m.policyOf, i.InstanceID)
					p.AttachedInstanceCount--
					m.detaches++
				}
			}
			writeMockEnvelope(w, http.StatusOK, true, nil, "", "")
		}
	})
	mux.HandleFunc("/autoscaler/v1/instances", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Instances []client.PolicyInstanceIdentifier `json:"instances"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		m.mu.Lock()
		defer m.mu.Unlock()
		out := []map[string]any{}
		for _, i := range body.Instances {
			var policy any
			if pid, ok := m.policyOf[i.InstanceID]; ok {
				if p, ok := m.policies[pid]; ok {
					policy = map[string]any{"scalingPolicyId": p.ID, "scalingPolicyName": p.Name}
				}
			}
			out = append(out, map[string]any{
				"instanceIdentifier": i,
				"managedPartition":   map[string]any{"instanceState": "running", "partitionEntries": []any{}, "scalingPolicyResponse": policy},
				"unmanagedPartition": map[string]any{"instanceState": nil, "instanceStatus": nil, "partitionEntries": []any{}},
			})
		}
		writeMockEnvelope(w, http.StatusOK, true, map[string]any{"instances": out}, "", "")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return client.NewClient(srv.URL, client.Credentials{RefreshToken: "test-token"}, client.WithHTTPClient(srv.Client())), m
}

func policyType() tftypes.Object {
	return tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"id": tftypes.String, "name": tftypes.String,
		"min_buffer_in_gb": tftypes.Number, "max_buffer_in_gb": tftypes.Number, "ideal_utilization_percent": tftypes.Number,
		"max_fs_size_in_gb": tftypes.Number, "min_disk_sku_size": tftypes.Number, "max_disk_sku_size": tftypes.Number,
		"attached_instance_count": tftypes.Number,
	}}
}

func policyValue(overrides map[string]tftypes.Value) tftypes.Value {
	v := map[string]tftypes.Value{
		"id": tftypes.NewValue(tftypes.String, nil), "name": strVal("prod-db-volumes"),
		"min_buffer_in_gb": numVal(30), "max_buffer_in_gb": numVal(300), "ideal_utilization_percent": numVal(80),
		"max_fs_size_in_gb": numVal(2000), "min_disk_sku_size": numVal(8), "max_disk_sku_size": numVal(64),
		"attached_instance_count": tftypes.NewValue(tftypes.Number, nil),
	}
	for k, o := range overrides {
		v[k] = o
	}
	return tftypes.NewValue(policyType(), v)
}

func schemaOf(t *testing.T, r resource.Resource) schema.Schema {
	t.Helper()
	var resp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &resp)
	return resp.Schema
}

func TestScalingPolicyLifecycle(t *testing.T) {
	c, m := newMockPolicyServer(t)
	r := &scalingPolicyResource{client: c}
	sch := schemaOf(t, r)
	ctx := context.Background()

	// Create: stored SKU sizes come from the follow-up GET, not the
	// create response's zeros.
	var created resource.CreateResponse
	created.State = tfsdk.State{Schema: sch}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Raw: policyValue(nil), Schema: sch}}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("Create: %+v", created.Diagnostics)
	}
	var st scalingPolicyResourceModel
	created.State.Get(ctx, &st)
	if st.ID.ValueString() != "33" || st.MinDiskSkuSize.ValueInt64() != 8 || st.MaxDiskSkuSize.ValueInt64() != 64 {
		t.Fatalf("got %+v, want id 33 and the stored SKU sizes 8/64", st)
	}

	// Read picks up drift made outside Terraform.
	m.policies[33].MaxBufferInGb = 999
	var read resource.ReadResponse
	read.State = tfsdk.State{Schema: sch}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	read.State.Get(ctx, &st)
	if st.MaxBufferInGb.ValueInt64() != 999 {
		t.Fatalf("drift not detected: got max_buffer_in_gb %d", st.MaxBufferInGb.ValueInt64())
	}

	// Update in place, including a rename.
	var updated resource.UpdateResponse
	updated.State = tfsdk.State{Schema: sch}
	plan := policyValue(map[string]tftypes.Value{"id": strVal("33"), "name": strVal("prod-db-volumes-v2"), "max_buffer_in_gb": numVal(400)})
	r.Update(ctx, resource.UpdateRequest{Plan: tfsdk.Plan{Raw: plan, Schema: sch}, State: read.State}, &updated)
	if updated.Diagnostics.HasError() || m.policies[33].Name != "prod-db-volumes-v2" || m.policies[33].MaxBufferInGb != 400 {
		t.Fatalf("Update: %+v / %+v", updated.Diagnostics, m.policies[33])
	}

	// Same name again: conflict names the existing policy's ID.
	var dup resource.CreateResponse
	dup.State = tfsdk.State{Schema: sch}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Raw: policyValue(map[string]tftypes.Value{"name": strVal("prod-db-volumes-v2")}), Schema: sch}}, &dup)
	if !dup.Diagnostics.HasError() || !strings.Contains(dup.Diagnostics.Errors()[0].Detail(), "import lucidity_scaling_policy.<name> 33") {
		t.Fatalf("expected a name conflict pointing at policy 33, got %+v", dup.Diagnostics)
	}

	// Delete, then Read removes it from state.
	var del resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &del)
	if del.Diagnostics.HasError() || len(m.policies) != 0 {
		t.Fatalf("Delete: %+v", del.Diagnostics)
	}
	var gone resource.ReadResponse
	gone.State = updated.State
	r.Read(ctx, resource.ReadRequest{State: updated.State}, &gone)
	if gone.Diagnostics.HasError() || !gone.State.Raw.IsNull() {
		t.Fatalf("expected removal from state, got %+v", gone.Diagnostics)
	}
}

func attachmentType() tftypes.Object {
	return tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"id": tftypes.String, "policy_id": tftypes.String, "instance_identifier": instanceIdentifierObjType(),
	}}
}

func instanceIdentifierObjType() tftypes.Object {
	return tftypes.Object{AttributeTypes: map[string]tftypes.Type{
		"cloud_provider": tftypes.String, "cloud_provider_account_id": tftypes.String, "resource_group": tftypes.String, "instance_id": tftypes.String,
	}}
}

func attachmentValue(policyID string, inst map[string]tftypes.Value) tftypes.Value {
	i := map[string]tftypes.Value{
		"cloud_provider": strVal("AWS"), "cloud_provider_account_id": strVal("123456789012"),
		"resource_group": tftypes.NewValue(tftypes.String, nil), "instance_id": strVal("i-0abc123def4567890"),
	}
	for k, v := range inst {
		i[k] = v
	}
	return tftypes.NewValue(attachmentType(), map[string]tftypes.Value{
		"id": tftypes.NewValue(tftypes.String, nil), "policy_id": strVal(policyID),
		"instance_identifier": tftypes.NewValue(instanceIdentifierObjType(), i),
	})
}

func TestScalingPolicyAttachmentLifecycle(t *testing.T) {
	c, m := newMockPolicyServer(t)
	m.policies[33] = &client.ScalingPolicy{ID: 33, ScalingPolicySettings: client.ScalingPolicySettings{Name: "p"}}
	r := &scalingPolicyAttachmentResource{client: c}
	sch := schemaOf(t, r)
	ctx := context.Background()

	var created resource.CreateResponse
	created.State = tfsdk.State{Schema: sch}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Raw: attachmentValue("33", nil), Schema: sch}}, &created)
	var st scalingPolicyAttachmentModel
	created.State.Get(ctx, &st)
	if created.Diagnostics.HasError() || !m.attached["33/i-0abc123def4567890"] || st.ID.ValueString() != "33/AWS/123456789012/i-0abc123def4567890" {
		t.Fatalf("Create: %+v, id %q", created.Diagnostics, st.ID.ValueString())
	}

	var del resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: created.State}, &del)
	if del.Diagnostics.HasError() || m.attached["33/i-0abc123def4567890"] {
		t.Fatalf("Delete: %+v", del.Diagnostics)
	}

	// Policy gone: Read drops the attachment; Delete is a no-op.
	delete(m.policies, 33)
	var read resource.ReadResponse
	read.State = created.State
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.IsNull() {
		t.Fatalf("expected removal when the policy is gone, got %+v", read.Diagnostics)
	}
	var del2 resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: created.State}, &del2)
	if del2.Diagnostics.HasError() {
		t.Fatalf("detach of a deleted policy must not error: %+v", del2.Diagnostics)
	}
}

func TestScalingPolicyAttachment_ImportAzureID(t *testing.T) {
	r := &scalingPolicyAttachmentResource{}
	sch := schemaOf(t, r)
	var resp resource.ImportStateResponse
	resp.State = tfsdk.State{Schema: sch, Raw: tftypes.NewValue(attachmentType(), nil)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "33/AZURE/sub-1/prod-rg/vm-db-01"}, &resp)
	var st scalingPolicyAttachmentModel
	resp.State.Get(context.Background(), &st)
	if resp.Diagnostics.HasError() || st.InstanceIdentifier.ResourceGroup.ValueString() != "prod-rg" || st.InstanceIdentifier.InstanceID.ValueString() != "vm-db-01" {
		t.Fatalf("got %+v / %+v", resp.Diagnostics, st.InstanceIdentifier)
	}
}

func validateResource(t *testing.T, typeName string, objType tftypes.Object, v tftypes.Value) []*tfprotov6.Diagnostic {
	t.Helper()
	dv, err := tfprotov6.NewDynamicValue(objType, v)
	if err != nil {
		t.Fatalf("NewDynamicValue: %v", err)
	}
	resp, err := newTestProviderServer(t).ValidateResourceConfig(context.Background(), &tfprotov6.ValidateResourceConfigRequest{TypeName: typeName, Config: &dv})
	if err != nil {
		t.Fatalf("ValidateResourceConfig: %v", err)
	}
	return resp.Diagnostics
}

func TestScalingPolicyRPC_Validation(t *testing.T) {
	if d := validateResource(t, "lucidity_scaling_policy", policyType(), policyValue(nil)); hasErrorDiagnostic(d) {
		t.Fatalf("valid policy rejected: %+v", d)
	}
	if d := validateResource(t, "lucidity_scaling_policy", policyType(), policyValue(map[string]tftypes.Value{"ideal_utilization_percent": numVal(0)})); !hasErrorDiagnostic(d) {
		t.Fatal("ideal_utilization_percent 0 must be rejected")
	}
	if d := validateResource(t, "lucidity_scaling_policy_attachment", attachmentType(), attachmentValue("33", nil)); hasErrorDiagnostic(d) {
		t.Fatalf("valid attachment rejected: %+v", d)
	}
	if d := validateResource(t, "lucidity_scaling_policy_attachment", attachmentType(), attachmentValue("33", map[string]tftypes.Value{"cloud_provider": strVal("AZURE")})); !hasErrorDiagnostic(d) {
		t.Fatal("AZURE without resource_group must be rejected")
	}
	if d := validateResource(t, "lucidity_scaling_policy_attachment", attachmentType(), attachmentValue("abc", nil)); !hasErrorDiagnostic(d) {
		t.Fatal("non-numeric policy_id must be rejected")
	}
}

func TestScalingPolicyRPC_RegistersResourcesAndDataSource(t *testing.T) {
	resp, err := newTestProviderServer(t).GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("GetProviderSchema: %v", err)
	}
	for _, n := range []string{"lucidity_scaling_policy", "lucidity_scaling_policy_attachment"} {
		if _, ok := resp.ResourceSchemas[n]; !ok {
			t.Errorf("resource %s not registered", n)
		}
	}
	if _, ok := resp.DataSourceSchemas["lucidity_scaling_policies"]; !ok {
		t.Error("data source lucidity_scaling_policies not registered")
	}
}

func readAttachment(t *testing.T, r *scalingPolicyAttachmentResource, st tfsdk.State) resource.ReadResponse {
	t.Helper()
	var resp resource.ReadResponse
	resp.State = st
	r.Read(context.Background(), resource.ReadRequest{State: st}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %+v", resp.Diagnostics)
	}
	return resp
}

// Read verifies the attachment against the instance's actual policy:
// unchanged keeps it; detached or switched elsewhere drops it (so the next
// plan re-attaches).
func TestScalingPolicyAttachment_ReadDetectsDrift(t *testing.T) {
	c, m := newMockPolicyServer(t)
	m.policies[33] = &client.ScalingPolicy{ID: 33, ScalingPolicySettings: client.ScalingPolicySettings{Name: "p"}}
	m.policies[34] = &client.ScalingPolicy{ID: 34, ScalingPolicySettings: client.ScalingPolicySettings{Name: "other"}}
	r := &scalingPolicyAttachmentResource{client: c}
	sch := schemaOf(t, r)
	ctx := context.Background()

	var created resource.CreateResponse
	created.State = tfsdk.State{Schema: sch}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Raw: attachmentValue("33", nil), Schema: sch}}, &created)

	if got := readAttachment(t, r, created.State); got.State.Raw.IsNull() {
		t.Fatal("an attachment that still holds must stay in state")
	}

	m.policyOf["i-0abc123def4567890"] = 34 // switched to another policy outside Terraform
	if got := readAttachment(t, r, created.State); !got.State.Raw.IsNull() || !strings.Contains(got.Diagnostics[0].Detail(), "scaling policy 34") {
		t.Fatalf("a switched policy must drop the attachment and name the new policy, got %+v", got.Diagnostics)
	}

	delete(m.policyOf, "i-0abc123def4567890") // detached outside Terraform
	if got := readAttachment(t, r, created.State); !got.State.Raw.IsNull() {
		t.Fatal("a detached instance must drop the attachment")
	}
}

// An import of an attachment that doesn't exist is caught by the Read that
// follows it.
func TestScalingPolicyAttachment_ImportIsVerified(t *testing.T) {
	c, m := newMockPolicyServer(t)
	m.policies[33] = &client.ScalingPolicy{ID: 33}
	r := &scalingPolicyAttachmentResource{client: c}
	sch := schemaOf(t, r)

	var imp resource.ImportStateResponse
	imp.State = tfsdk.State{Schema: sch, Raw: tftypes.NewValue(attachmentType(), nil)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "33/AWS/123456789012/i-not-attached"}, &imp)
	if imp.Diagnostics.HasError() {
		t.Fatalf("ImportState: %+v", imp.Diagnostics)
	}
	if got := readAttachment(t, r, imp.State); !got.State.Raw.IsNull() {
		t.Fatal("importing an attachment that doesn't exist must end with nothing in state")
	}
}
