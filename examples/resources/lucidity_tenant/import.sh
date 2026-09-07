# Import ID is "<cloud_provider>/<cloud_provider_account_id>".
# This is also the ONLY way an AZURE or GCP tenant enters Terraform, since
# onboarding a brand-new resource is AWS-only.
terraform import lucidity_tenant.example AWS/123456789012
