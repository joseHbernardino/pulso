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
                  default = "${config.home.homeDirectory}/.pulso";
                  description = "Pulso data directory.";
                };
                socket_path = lib.mkOption {
                  type = lib.types.str;
                  default = "${config.home.homeDirectory}/.pulso/tmp/pulso.sock";
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
  };

  config = lib.mkIf cfg.enable {
    home.packages = [ cfg.package ];

    xdg.configFile."pulso/pulso.yaml".source =
      settingsFormat.generate "pulso.yaml" cfg.settings;

    systemd.user.services.pulso = {
      Unit = {
        Description = "Pulso Process Manager";
        After = [ "default.target" ];
      };

      Service = {
        ExecStart = "${lib.getExe cfg.package} --data-dir ${cfg.settings.daemon.data_dir} daemon run --web --config ${config.xdg.configHome}/pulso/pulso.yaml";
        Restart = "on-failure";
        RestartSec = "5s";
      };

      Install.WantedBy = [ "default.target" ];
    };
  };
}
