data "lucidity_roles" "all" {}

output "role_names" {
  value = [for r in data.lucidity_roles.all.roles : r.name]
}
