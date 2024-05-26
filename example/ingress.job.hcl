## Run the ingress proxy service
job "ingress" {
  type = "service"

  group "main" {
    network {
      mode = "bridge"
      port "http" {
        static = 80
        to     = 9876
      }
    }

    service {
      provider = "nomad"

      name = "nimby"
      port = "http"
    }

    task "proxy" {
      driver = "docker"

      identity {
        file = true
        change_mode = "signal"
        change_signal = "SIGHUP"
      }

      config {
        image = "ghcr.io/jmanero/nimby"
        ports = ["http"]

        security_opt = [
          ## Policy to permit connection to a unix socket appears to be omitted in policy from container-selinux-2.230.0
          ## avc:  denied  { connectto } ... comm="curl" path="/var/data/nomad/alloc/.../secrets/api.sock" scontext=system_u:system_r:container_t:s0:... tcontext=system_u:system_r:unconfined_service_t:s0 tclass=unix_stream_socket permissive=0
          "label=disable"
        ]
      }
    }
  }
}
