# Managing service accounts needs an account admin's refresh token: a
# service account's own credentials can only read.
resource "lucidity_service_account" "ci" {
  name        = "ci-deploy-bot"
  description = "GitHub Actions deploy pipeline"

  # Roles per tenant. This is the complete set: a tenant left out has no access.
  permissions = {
    (lucidity_tenant.prod.tenant_id) = ["Viewer", "Editor"]
    (lucidity_tenant.dev.tenant_id)  = ["Viewer"]
  }

  # Deleting a service account is permanent and its name can't be reused.
  lifecycle {
    prevent_destroy = true
  }
}
