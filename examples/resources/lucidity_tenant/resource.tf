# Onboarding a new AWS account. Lucidity's tenant API supports AWS accounts
# only for now; Azure and GCP will follow in a later Lucidity release.
resource "lucidity_tenant" "example" {
  lucidity_dashboard_display_name = "example-account"
  lucidity_product_list           = ["AUTOSCALER"]

  cloud_entity_information {
    cloud_provider            = "AWS"
    cloud_provider_account_id = "123456789012"
    aws_iam_external_id       = "8f14e45f-ceea-4331-9f5e-111111111111" # generate your own; must match the IAM role's trust policy
    aws_iam_role_name         = "LucidityRole"
    aws_iam_policy_name       = "LucidityPolicy"
  }

  # Safety rails — defaults shown explicitly for visibility in code review.
  # Deboarding on Lucidity is IRREVERSIBLE via API: an INACTIVE tenant can
  # only be reactivated by Lucidity support, and deboarding an account with
  # running services causes immediate disruption.
  lucidity_dashboard_account_delete_protection = true
  # lucidity_account_destroy_behavior = "forget"   # consulted only if protection = false
}
