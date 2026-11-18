# Every active user.
data "lucidity_users" "active" {
  status = "ACTIVE"
}

# Users holding the Editor role on any cloud account.
data "lucidity_users" "editors" {
  role = "Editor"
}

output "admin_emails" {
  value = [for u in data.lucidity_users.active.users : u.email if u.is_account_admin]
}
