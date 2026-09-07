# ─────────────────────────────────────────────────────────────────────────────
# Lucidity Tenant Management — Reference Configuration
#
# One resource block per cloud account, written plainly — no locals map, no
# for_each, no data-structure abstraction standing in for the config itself.
#
# aws_iam_external_id must be a value YOU generate (a UUID works) and place in the
# target AWS IAM role's trust policy before onboarding — Lucidity sends it on
# every AssumeRole so the role only trusts requests carrying it. It cannot be
# changed after onboarding (see CLAUDE.md), so double-check it before apply.
# ─────────────────────────────────────────────────────────────────────────────

# ─── non-prod ────────────────────────────────────────────────────────────────

resource "lucidity_tenant" "non_prod_1" {
  lucidity_dashboard_display_name = "non-prod"
  lucidity_product_list           = ["AUTOSCALER"]

  cloud_entity_information {
    cloud_provider            = "AWS"
    cloud_provider_account_id = "111111111111"
    aws_iam_external_id       = "8f14e45f-ceea-4331-9f5e-111111111111"
    aws_iam_role_name         = "LucidityRole"
    aws_iam_policy_name       = "LucidityPolicy"
  }

  # Safety rails — defaults shown explicitly for visibility in code review.
  # ⚠️  Deboarding on Lucidity is IRREVERSIBLE via API. An INACTIVE tenant can
  #     only be reactivated by Lucidity support, and deboarding an account
  #     with running services causes immediate disruption.
  lucidity_dashboard_account_delete_protection = true
  # lucidity_account_destroy_behavior = "forget"   # consulted only if protection = false
}

resource "lucidity_tenant" "non_prod_2" {
  lucidity_dashboard_display_name = "non-prod"
  lucidity_product_list           = ["AUTOSCALER"]

  cloud_entity_information {
    cloud_provider            = "AWS"
    cloud_provider_account_id = "222222222222"
    aws_iam_external_id       = "8f14e45f-ceea-4331-9f5e-222222222222"
    aws_iam_role_name         = "LucidityRole"
    aws_iam_policy_name       = "LucidityPolicy"
  }

  lucidity_dashboard_account_delete_protection = true
}

resource "lucidity_tenant" "non_prod_3" {
  lucidity_dashboard_display_name = "non-prod"
  lucidity_product_list           = ["AUTOSCALER"]

  cloud_entity_information {
    cloud_provider            = "AWS"
    cloud_provider_account_id = "333333333333"
    aws_iam_external_id       = "8f14e45f-ceea-4331-9f5e-333333333333"
    aws_iam_role_name         = "LucidityRole"
    aws_iam_policy_name       = "LucidityPolicy"
  }

  lucidity_dashboard_account_delete_protection = true
}

# ─── qa ──────────────────────────────────────────────────────────────────────
# Both accounts below assume the same shared IAM role/policy under one AWS
# Organization — aws_root_account_id records which org root they roll up to, for
# audit purposes. It is sent to Lucidity best-effort but isn't part of the
# documented API (see CLAUDE.md) and isn't used for grouping or resolution —
# the tenant is still identified purely by cloud_provider +
# cloud_provider_account_id.

resource "lucidity_tenant" "qa_1" {
  lucidity_dashboard_display_name = "qa"
  lucidity_product_list           = ["AUTOSCALER"]
  aws_root_account_id             = "999999999999"

  cloud_entity_information {
    cloud_provider            = "AWS"
    cloud_provider_account_id = "444444444444"
    aws_iam_external_id       = "8f14e45f-ceea-4331-9f5e-444444444444"
    aws_iam_role_name         = "LucidityRole"
    aws_iam_policy_name       = "LucidityPolicy"
  }

  lucidity_dashboard_account_delete_protection = true
}

resource "lucidity_tenant" "qa_2" {
  lucidity_dashboard_display_name = "qa"
  lucidity_product_list           = ["AUTOSCALER"]
  aws_root_account_id             = "999999999999"

  cloud_entity_information {
    cloud_provider            = "AWS"
    cloud_provider_account_id = "555555555555"
    aws_iam_external_id       = "8f14e45f-ceea-4331-9f5e-555555555555"
    aws_iam_role_name         = "LucidityRole"
    aws_iam_policy_name       = "LucidityPolicy"
  }

  lucidity_dashboard_account_delete_protection = true
}
