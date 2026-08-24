# ─────────────────────────────────────────────────────────────────────────────
# Lucidity Tenant Management — Reference Configuration
#
# Input format: environments map (grouped by dashboard display name),
#               mirroring how tenants appear on the Lucidity dashboard.
# Resources:    one lucidity_tenant per account (for_each keyed by account ID)
#               so every account has an independent lifecycle, protection
#               flags, and status.
# Outputs:      (1) structured grouped view, (2) human-readable dashboard
#               summary — both rendered fully at PLAN time so reviewers see
#               the intended dashboard state before anything is applied.
# ─────────────────────────────────────────────────────────────────────────────

# ─── Input: edit ONLY this map to onboard/offboard accounts ─────────────────
#
# NOTE: keys must be unique. Accounts sharing a display name on the Lucidity
# dashboard should also carry the org root id (aws_root_id) — the Lucidity
# API requires it when multiple tenants share a dashboard name.

locals {
  environments = {
    "non-prod" = {
      aws_root_id = "r-abcd" # required when multiple accounts share a display name
      accounts = [
        { account_id = "111111111111", role_name = "LucidityRole", policy_name = "LucidityPolicy" },
        { account_id = "222222222222", role_name = "LucidityRole", policy_name = "LucidityPolicy" },
        { account_id = "333333333333", role_name = "LucidityRole", policy_name = "LucidityPolicy" },
      ]
    }
    "qa" = {
      aws_root_id = "r-abcd"
      accounts = [
        { account_id = "444444444444", role_name = "LucidityRole", policy_name = "LucidityPolicy" },
        { account_id = "555555555555", role_name = "LucidityRole", policy_name = "LucidityPolicy" },
      ]
    }
  }
}

# ─── Flatten: one entry per account, keyed by account ID ────────────────────
#
# Keying by account_id (not by name or list index) is deliberate:
# reordering the list or renaming an environment never makes Terraform
# think an account was removed — which, given deboard irreversibility,
# is a safety feature, not a style choice. Moving an account between
# environments diffs only as a display_name update (in-place once the
# friendly-name update API is live), never a destroy/create.

locals {
  tenants = merge([
    for env_name, env in local.environments : {
      for acc in env.accounts : acc.account_id => {
        display_name = env_name
        aws_root_id  = env.aws_root_id
        account_id   = acc.account_id
        role_name    = acc.role_name
        policy_name  = acc.policy_name
      }
    }
  ]...)
}

# ─── Resources: one lucidity_tenant per account ─────────────────────────────
#
# ⚠️  Deboarding on Lucidity is IRREVERSIBLE via API. An INACTIVE tenant can
#     only be reactivated by Lucidity support, and deboarding an account with
#     running services causes immediate disruption. account_delete_protection
#     stays true unless you have deliberately decided otherwise. See the
#     lucidity_tenant resource docs before changing destroy behavior.

resource "lucidity_tenant" "this" {
  for_each = local.tenants

  display_name = each.value.display_name
  product_list = ["AUTOSCALER"]

  cloud_entity_information {
    cloud_provider            = "AWS"
    cloud_provider_account_id = each.value.account_id
    aws_iam_role_name         = each.value.role_name
    aws_iam_policy_name       = each.value.policy_name
    aws_root_id               = each.value.aws_root_id
  }

  # Safety rails — defaults shown explicitly for visibility in code review.
  account_delete_protection = true
  # destroy_behavior = "forget"   # consulted only if protection = false
}

# ─── Output 1: structured grouped view (machine-readable) ───────────────────
#
# Mirrors the input grouping — how tenants will appear on the Lucidity
# dashboard. Useful for tooling, CI assertions, or feeding other modules.

output "lucidity_dashboard_view" {
  description = "Tenants grouped by Lucidity dashboard display name (intended state)"
  value = {
    for env_name, env in local.environments :
    env_name => [
      for acc in env.accounts : {
        account_id  = acc.account_id
        role_name   = acc.role_name
        policy_name = acc.policy_name
      }
    ]
  }
}

# ─── Output 2: pretty-printed dashboard summary (human-readable) ────────────
#
# Renders in every plan/apply so PR reviewers see the intended dashboard
# state — including per-environment account counts, which makes an
# accidentally deleted line immediately visible.

output "lucidity_dashboard_summary" {
  description = "Human-readable preview of the Lucidity dashboard layout"
  value = join("\n", flatten([
    for env_name, env in local.environments : concat(
      ["", "displayName: ${env_name}  (${length(env.accounts)} accounts)"],
      [for acc in env.accounts :
        "    ${acc.account_id}  role=${acc.role_name}  policy=${acc.policy_name}"
      ]
    )
  ]))
}
