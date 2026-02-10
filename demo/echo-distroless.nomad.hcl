# Echo Server - Distroless Envoy Sidecar Test
# Same as echo.nomad.hcl but uses envoyproxy/envoy-distroless for sidecars.
# The distroless image has no shell (no bash, no sh, no curl, no wget).
# xDSnap should fall back to sibling tasks (server/nginx) for Envoy admin access.

job "echo-distroless" {
  datacenters = ["dc1"]

  group "api" {
    network {
      mode = "bridge"
    }

    service {
      name = "echo-api-distroless"
      port = "8080"

      connect {
        sidecar_service {}

        sidecar_task {
          config {
            image = "envoyproxy/envoy-distroless:v1.35-latest"
          }
        }
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
        static = 9003
        to     = 80
      }
    }

    service {
      name = "echo-client-distroless"
      port = "http"

      connect {
        sidecar_service {
          proxy {
            upstreams {
              destination_name = "echo-api-distroless"
              local_bind_port  = 8080
            }
          }
        }

        sidecar_task {
          config {
            image = "envoyproxy/envoy-distroless:v1.35-latest"
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
