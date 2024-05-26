
job "demo_service" {
  type = "serivce"

  group "main" {
    count = 3

    network {
      mode = "bridge"
      port "http" {
        to 80
      }
    }

    service {
      provider = "nomad"

      name = "demo"
      port = "http"

      tags = [
        ## Nimby will route HTTP requests to demo.nimby.app to instances of this service
        "nimby-domain:demo.nimby.app"
        "nimby-protocol:http",
      ]
    }

    task "server" {
      driver = "docker"

      config {
        image = "docker.io/library/nginx"
        ports = ["http"]
      }
    }
  }
}
