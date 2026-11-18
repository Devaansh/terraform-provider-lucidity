# One attachment per instance; a policy can be attached to many instances.
resource "lucidity_scaling_policy_attachment" "orders_db" {
  policy_id = lucidity_scaling_policy.db.id

  instance_identifier = {
    cloud_provider            = "AWS"
    cloud_provider_account_id = "123456789012"
    instance_id               = "i-0abc123def4567890"
  }
}

# Azure needs the resource group; instance_id is the VM name.
resource "lucidity_scaling_policy_attachment" "azure_vm" {
  policy_id = lucidity_scaling_policy.db.id

  instance_identifier = {
    cloud_provider            = "AZURE"
    cloud_provider_account_id = "8f2d1c3a-5b6e-4f70-9a81-2c3d4e5f6a7b"
    resource_group            = "prod-rg"
    instance_id               = "vm-db-01"
  }
}
