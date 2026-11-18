data "lucidity_service_accounts" "all" {}

output "disabled_service_accounts" {
  value = [for sa in data.lucidity_service_accounts.all.service_accounts : sa.name if !sa.enabled]
}
