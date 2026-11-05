data "lucidity_scaling_policies" "all" {}

output "policy_ids_by_name" {
  value = { for p in data.lucidity_scaling_policies.all.policies : p.name => p.id }
}
