resource "lucidity_scaling_policy" "db" {
  name                      = "prod-db-volumes"
  min_buffer_in_gb          = 30
  max_buffer_in_gb          = 300
  ideal_utilization_percent = 80
  max_fs_size_in_gb         = 2000
  min_disk_sku_size         = 8
  max_disk_sku_size         = 64
}
