data "lucidity_tenants" "all" {}

output "active_tenant_ids" {
  value = [
    for t in data.lucidity_tenants.all.tenants : t.tenant_id
    if t.status == "ACTIVE"
  ]
}
