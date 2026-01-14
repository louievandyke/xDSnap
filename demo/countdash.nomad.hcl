# Echo Server - Consul Connect Demo (ARM64 compatible)
# This deploys two services connected via Consul service mesh:
#   - echo-api: Backend echo server
#   - echo-client: Frontend that calls the backend

job "echo" {
  datacenters = ["dc1"]

  group "api" {
    network {
      mode = "bridge"
    }

    service {
      name = "echo-api"
      port = "8080"

      connect {
        sidecar_service {}
      }
    }

    task "server" {
      driver = "docker"

      config {
        image = "ealen/echo-server:latest"
        ports = ["http"]
      }

      env {
        PORT = "8080"
      }
    }
  }

  group "client" {
    network {
      mode = "bridge"

      port "http" {
        static = 9002
        to     = 80
      }
    }

    service {
      name = "echo-client"
      port = "http"

      connect {
        sidecar_service {
          proxy {
            upstreams {
              destination_name = "echo-api"
              local_bind_port  = 8080
            }
          }
        }
      }
    }

    task "nginx" {
      driver = "docker"

      config {
        image = "nginx:alpine"
        ports = ["http"]
        volumes = [
          "local/default.conf:/etc/nginx/conf.d/default.conf"
        ]
      }

      template {
        data = <<EOF
server {
    listen 80;
    location / {
        proxy_pass http://127.0.0.1:8080;
    }
}
EOF
        destination = "local/default.conf"
      }
    }
  }
}
