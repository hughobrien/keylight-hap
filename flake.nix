{
  description = "keylight-hap — exposes Elgato Key Lights to HomeKit via mDNS discovery";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }: let
    systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
    forEachSystem = f: nixpkgs.lib.genAttrs systems f;

    perSystem = forEachSystem (system: let
      pkgs = import nixpkgs { inherit system; };
      version = "0.1.0";

      keylight-hap-pkg = pkgs.buildGoModule {
        pname = "keylight-hap";
        inherit version;
        src = ./.;
        # NAR hash of this module set. To refresh after changing deps: set to
        # pkgs.lib.fakeHash, run `nix build .#keylight-hap`, paste the
        # reported `got:` hash here.
        vendorHash = "sha256-EAWXkIN1rt7Q83+25g1rU6K3CpnTnfZguqgKedrk4ZI=";
        subPackages = [ "cmd/keylight-hap" ];
        ldflags = [ "-s" "-w" ];
        doCheck = true;
        meta = with pkgs.lib; {
          description = "Exposes Elgato Key Lights to HomeKit";
          homepage = "https://github.com/hughobrien/keylight-hap";
          license = licenses.gpl3Plus;
          platforms = platforms.unix;
          mainProgram = "keylight-hap";
        };
      };
    in {
      packages = {
        default = keylight-hap-pkg;
        keylight-hap = keylight-hap-pkg;
      };

      apps.default = {
        type = "app";
        program = "${keylight-hap-pkg}/bin/keylight-hap";
      };

      devShells.default = pkgs.mkShell {
        packages = with pkgs; [ go gopls gotools go-tools ];
      };

      formatter = pkgs.nixpkgs-fmt;
    });

    defaultModule = { pkgs, lib, ... }: {
      imports = [ ./nix/module.nix ];
      services.keylight-hap.package = lib.mkDefault
        self.packages.${pkgs.stdenv.hostPlatform.system}.default;
    };
  in {
    nixosModules.default = defaultModule;
    nixosModules.keylight-hap = defaultModule;

    packages   = forEachSystem (system: perSystem.${system}.packages);
    apps       = forEachSystem (system: perSystem.${system}.apps);
    devShells  = forEachSystem (system: perSystem.${system}.devShells);
    formatter  = forEachSystem (system: perSystem.${system}.formatter);
  };
}
