## Run the ingress proxy service
job "ingress" {
  type = "service"

  group "main" {
    network {
      mode = "bridge"
      port "http" {
        static 80
        to 9876
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
        ## Provide a NOMAD_TOKEN environment variable for use with the
        ## /secrets/api.sock listener
        env = true

        change_mode = "signal"
        change_signal = "SIGHUP"
      }

      config {
        image = "ghcr.io/jmanero/nimby"
        ports = ["http"]
      }
    }
  }
}
