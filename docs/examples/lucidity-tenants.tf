# ─────────────────────────────────────────────────────────────────────────────
# Lucidity Tenant Management — Reference Configuration
#
# One resource block per cloud account, written plainly — no locals map, no
# for_each, no data-structure abstraction standing in for the config itself.
# ─────────────────────────────────────────────────────────────────────────────

# ─── non-prod ────────────────────────────────────────────────────────────────

resource "lucidity_tenant" "non_prod_1" {
  display_name = "non-prod"
  product_list = ["AUTOSCALER"]

  cloud_entity_information {
    cloud_provider            = "AWS"
    cloud_provider_account_id = "111111111111"
    aws_iam_role_name         = "LucidityRole"
    aws_iam_policy_name       = "LucidityPolicy"
  }

  # Safety rails — defaults shown explicitly for visibility in code review.
  # ⚠️  Deboarding on Lucidity is IRREVERSIBLE via API. An INACTIVE tenant can
  #     only be reactivated by Lucidity support, and deboarding an account
  #     with running services causes immediate disruption.
  account_delete_protection = true
  # destroy_behavior = "forget"   # consulted only if protection = false
}

resource "lucidity_tenant" "non_prod_2" {
  display_name = "non-prod"
  product_list = ["AUTOSCALER"]

  cloud_entity_information {
    cloud_provider            = "AWS"
    cloud_provider_account_id = "222222222222"
    aws_iam_role_name         = "LucidityRole"
    aws_iam_policy_name       = "LucidityPolicy"
  }

  account_delete_protection = true
}

resource "lucidity_tenant" "non_prod_3" {
  display_name = "non-prod"
  product_list = ["AUTOSCALER"]

  cloud_entity_information {
    cloud_provider            = "AWS"
    cloud_provider_account_id = "333333333333"
    aws_iam_role_name         = "LucidityRole"
    aws_iam_policy_name       = "LucidityPolicy"
  }

  account_delete_protection = true
}

# ─── qa ──────────────────────────────────────────────────────────────────────

resource "lucidity_tenant" "qa_1" {
  display_name = "qa"
  product_list = ["AUTOSCALER"]

  cloud_entity_information {
    cloud_provider            = "AWS"
    cloud_provider_account_id = "444444444444"
    aws_iam_role_name         = "LucidityRole"
    aws_iam_policy_name       = "LucidityPolicy"
  }

  account_delete_protection = true
}

resource "lucidity_tenant" "qa_2" {
  display_name = "qa"
  product_list = ["AUTOSCALER"]

  cloud_entity_information {
    cloud_provider            = "AWS"
    cloud_provider_account_id = "555555555555"
    aws_iam_role_name         = "LucidityRole"
    aws_iam_policy_name       = "LucidityPolicy"
  }

  account_delete_protection = true
}
