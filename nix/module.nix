# SPDX-License-Identifier: GPL-3.0-or-later
#
# NixOS module for keylight-hap.
#
#   {
#     imports = [ inputs.keylight-hap.nixosModules.default ];
#     services.keylight-hap = {
#       enable = true;
#       openFirewall = true;
#     };
#   }

{ config, lib, pkgs, ... }:

let
  cfg = config.services.keylight-hap;
in {
  options.services.keylight-hap = {
    enable = lib.mkEnableOption "keylight-hap — Elgato Key Light to HomeKit bridge";

    package = lib.mkOption {
      type = lib.types.package;
      default = pkgs.keylight-hap or (throw
        "services.keylight-hap.package is unset. Set it explicitly, e.g. `services.keylight-hap.package = inputs.keylight-hap.packages.\${pkgs.system}.default;`.");
      defaultText = lib.literalExpression "pkgs.keylight-hap";
      description = "The keylight-hap package providing /bin/keylight-hap.";
    };

    bridgeName = lib.mkOption {
      type = lib.types.str;
      default = "keylight-hap";
      description = "Name shown in iOS during HomeKit pairing.";
    };

    port = lib.mkOption {
      type = lib.types.port;
      default = 0;
      description = "HAP TCP port. 0 = ephemeral (OS-assigned). Pin a port if the firewall needs a fixed hole.";
    };

    pollInterval = lib.mkOption {
      type = lib.types.str;
      default = "20s";
      description = "How often to poll each light to reflect out-of-band changes in HomeKit.";
    };

    discoveryTimeout = lib.mkOption {
      type = lib.types.str;
      default = "5s";
      description = "mDNS browse window per discovery attempt at startup.";
    };

    stateDir = lib.mkOption {
      type = lib.types.path;
      default = "/var/lib/keylight-hap";
      description = ''
        Directory where the HAP server persists pairing keys + the generated
        PIN. Delete to factory-reset HomeKit pairing. Must reside under
        /var/lib/keylight-hap (the StateDirectory) to be writable under
        ProtectSystem=strict.
      '';
    };

    user = lib.mkOption {
      type = lib.types.str;
      default = "keylight-hap";
      description = "System user the daemon runs as.";
    };

    group = lib.mkOption {
      type = lib.types.str;
      default = "keylight-hap";
      description = "System group the daemon runs as.";
    };

    openFirewall = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Open the HAP TCP port (when pinned) and mDNS UDP 5353 in the firewall.
        HomeKit needs inbound UDP/5353 for iPhones to discover the bridge.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    users.users.${cfg.user} = {
      isSystemUser = true;
      group = cfg.group;
      description = "keylight-hap daemon user";
    };
    users.groups.${cfg.group} = { };

    systemd.services.keylight-hap = {
      description = "Elgato Key Light to HomeKit bridge";
      wants = [ "network-online.target" ];
      after = [ "network-online.target" ];
      wantedBy = [ "multi-user.target" ];

      serviceConfig = {
        ExecStart = lib.concatStringsSep " " [
          "${cfg.package}/bin/keylight-hap"
          "--bridge-name" (lib.escapeShellArg cfg.bridgeName)
          "--port" (toString cfg.port)
          "--poll-interval" cfg.pollInterval
          "--discovery-timeout" cfg.discoveryTimeout
          "--state-dir" cfg.stateDir
        ];
        User = cfg.user;
        Group = cfg.group;
        Restart = "on-failure";
        RestartSec = "5s";
        StateDirectory = "keylight-hap";

        # Hardening. AF_NETLINK is REQUIRED: Go's net.Interfaces() (used by the
        # hap mDNS responder) needs it, or the bridge advertises on zero
        # interfaces and is invisible to iPhones — silently.
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectKernelLogs = true;
        ProtectControlGroups = true;
        ProtectClock = true;
        ProtectHostname = true;
        ProtectProc = "invisible";
        RestrictAddressFamilies = [ "AF_INET" "AF_INET6" "AF_UNIX" "AF_NETLINK" ];
        RestrictNamespaces = true;
        RestrictRealtime = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        SystemCallArchitectures = "native";
        SystemCallFilter = [ "@system-service" "~@privileged" ];
      };
    };

    networking.firewall = lib.mkIf cfg.openFirewall {
      allowedUDPPorts = [ 5353 ];
      allowedTCPPorts = lib.optional (cfg.port != 0) cfg.port;
    };
  };
}
