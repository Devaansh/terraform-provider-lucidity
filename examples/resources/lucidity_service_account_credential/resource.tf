resource "lucidity_service_account_credential" "ci" {
  service_account_id = lucidity_service_account.ci.id
  name               = "github-actions"
  # expires_at       = "2027-06-30T00:00:00Z" # defaults to 90 days after creation
}

# Send the secret straight to a secrets manager instead of outputting it.
# When the credential expires or is revoked, the next apply creates a new
# one and this secret updates with it.
resource "aws_secretsmanager_secret_version" "lucidity_ci" {
  secret_id = "lucidity/ci-deploy-bot"
  secret_string = jsonencode({
    client_id     = lucidity_service_account_credential.ci.client_id
    client_secret = lucidity_service_account_credential.ci.client_secret
  })
}
