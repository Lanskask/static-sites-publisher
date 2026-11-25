# A throwaway Google Compute Engine VM for testing the README's self-hosting steps against real DNS, real
# Let's Encrypt and real OAuth. It is a plain Debian server with Docker, nothing GCP-specific is used by the app.
#
#   make tf-plan-apply GCP_PROJECT=my-project     # shows the plan, asks, creates; prints the IP and the ssh target
#   ... follow README "Self-hosting" with HOST=<ssh target> ...
#   make tf-destroy GCP_PROJECT=my-project
#
# e2-micro in us-central1 is in GCP's free tier (one per billing account); otherwise it costs about $0.01 an hour.

terraform {
  required_providers {
    google = { source = "hashicorp/google", version = "~> 6.0" }
  }
}

variable "project" { type = string }
variable "zone" { default = "us-central1-a" }
variable "machine_type" { default = "e2-micro" } # 2 shared vCPU, 1 GB: checks the README's "small server" claim
variable "ssh_user" { default = "sites" }
variable "ssh_pubkey" { default = "~/.ssh/id_ed25519.pub" }

provider "google" {
  project = var.project
  zone    = var.zone
}

resource "google_project_service" "compute" {
  service            = "compute.googleapis.com"
  disable_on_destroy = false # turning it off again is slow and can break other things in the project
}

resource "google_compute_network" "e2e" {
  name                    = "sites-e2e"
  auto_create_subnetworks = true
  depends_on              = [google_project_service.compute]
}

resource "google_compute_firewall" "web" {
  name          = "sites-e2e-web"
  network       = google_compute_network.e2e.name
  source_ranges = ["0.0.0.0/0"]
  allow {
    protocol = "tcp"
    ports    = ["22", "80", "443"]
  }
}

resource "google_compute_instance" "e2e" {
  name         = "sites-e2e"
  machine_type = var.machine_type
  boot_disk {
    initialize_params {
      image = "debian-cloud/debian-12"
      size  = 10
    }
  }
  network_interface {
    network = google_compute_network.e2e.name
    access_config {} # ephemeral public IPv4
  }
  metadata = {
    ssh-keys = "${var.ssh_user}:${trimspace(file(pathexpand(var.ssh_pubkey)))}"
  }
  # The README's server prerequisite: Docker with the compose plugin, usable by the ssh user. Swap so that
  # building the image on a 1 GB machine does not run out of memory.
  metadata_startup_script = <<-EOT
    #!/bin/sh
    set -e
    [ -f /swapfile ] || { fallocate -l 1G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile; }
    command -v docker >/dev/null || curl -fsSL https://get.docker.com | sh
    until id ${var.ssh_user} >/dev/null 2>&1; do sleep 2; done # the guest agent creates it from ssh-keys
    usermod -aG docker ${var.ssh_user}
    touch /var/lib/sites-e2e-ready
  EOT
}

output "ip" { value = google_compute_instance.e2e.network_interface[0].access_config[0].nat_ip }
output "ssh" { value = "${var.ssh_user}@${google_compute_instance.e2e.network_interface[0].access_config[0].nat_ip}" }
