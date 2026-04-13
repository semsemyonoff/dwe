{
  "name": "{{ .Project.Name }}",
  "dockerComposeFile": ["../../../compose.yaml"],
  "service": "{{ .ServiceCfg.Container }}",
  "workspaceFolder": "{{ .ServiceCfg.DirInternal }}",
  "remoteUser": "www-data",
  "customizations": {
    "vscode": {
      "extensions": [
        "xdebug.php-debug",
        "bmewburn.vscode-intelephense-client",
        "mikestead.dotenv"
      ],
      "settings": {
        "php.validate.executablePath": "/usr/local/bin/php"
      }
    }
  },
  "forwardPorts": [{{ .Runtime.Ports.App }}],
  "postCreateCommand": "composer install --no-interaction"
}
