self:
{
  config,
  lib,
  pkgs,
  ...
}:
let
  cfg = config.services.pulso;
  settingsFormat = pkgs.formats.yaml { };
in
{
  options.services.pulso = {
    enable = lib.mkEnableOption "Pulso process manager";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.stdenv.hostPlatform.system}.default;
      defaultText = lib.literalExpression "self.packages.\${system}.default";
      description = "The pulso package to use.";
    };

    settings = lib.mkOption {
      type = lib.types.submodule {
        freeformType = settingsFormat.type;
        options = {
          daemon = lib.mkOption {
            type = lib.types.submodule {
              freeformType = settingsFormat.type;
              options = {
                data_dir = lib.mkOption {
                  type = lib.types.str;
                  default = "/var/lib/pulso";
                  description = "Pulso data directory.";
                };
                socket_path = lib.mkOption {
                  type = lib.types.str;
                  default = "/run/pulso/pulso.sock";
                  description = "Unix socket path for IPC.";
                };
                log_level = lib.mkOption {
                  type = lib.types.enum [
                    "trace"
                    "debug"
                    "info"
                    "warn"
                    "error"
                    "fatal"
                    "panic"
                  ];
                  default = "info";
                  description = "Log level.";
                };
              };
            };
            default = { };
            description = "Daemon settings.";
          };
        };
      };
      default = { };
      description = "Pulso configuration (pulso.yaml).";
    };

    configDir = lib.mkOption {
      type = lib.types.str;
      default = "/etc/pulso";
      description = "Directory for pulso.yaml config file.";
    };
  };

  config = lib.mkIf cfg.enable {
    users.users.pulso = {
      isSystemUser = true;
      group = "pulso";
      home = cfg.settings.daemon.data_dir;
      createHome = true;
    };

    users.groups.pulso = { };

    environment.etc."pulso/pulso.yaml".source =
      settingsFormat.generate "pulso.yaml" cfg.settings;

    systemd.services.pulso = {
      description = "Pulso Process Manager";
      wantedBy = [ "multi-user.target" ];
      after = [ "network.target" ];

      serviceConfig = {
        Type = "notify";
        ExecStart = "${lib.getExe cfg.package} daemon start --config ${cfg.configDir}/pulso.yaml";
        Restart = "on-failure";
        RestartSec = "5s";

        User = "pulso";
        Group = "pulso";

        RuntimeDirectory = "pulso";
        RuntimeDirectoryMode = "0750";
        StateDirectory = "pulso";
        WorkingDirectory = cfg.settings.daemon.data_dir;

        LimitNOFILE = "65536";
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
      };
    };
  };
}
