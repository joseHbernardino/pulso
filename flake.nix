{
  description = "Pulso — A modern process manager and application supervisor";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    # Nixpkgs 26.11 removed Intel macOS; keep its supported security branch.
    nixpkgs-intel.url = "github:NixOS/nixpkgs/nixpkgs-26.05-darwin";
    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      self,
      nixpkgs,
      nixpkgs-intel,
      home-manager,
    }:
    let
      # self.shortRev existe apenas num flake git. Um flake `path:` — que
      # scripts/update-vendor-hash.sh usa para calcular o vendorHash — e um
      # tarball de código não têm revisão, e algumas versões do Nix tampouco
      # expõem lastModifiedDate nesse caso. Sem as reservas a avaliação aborta
      # com "attribute 'lastModifiedDate' missing", e nem o hash nem um build
      # a partir do código-fonte avulso seriam possíveis.
      version =
        if self ? shortRev then
          self.shortRev
        else if self ? lastModifiedDate then
          builtins.substring 0 8 self.lastModifiedDate
        else
          "dev";

      pkgsFor = system:
        if system == "x86_64-darwin" then
          nixpkgs-intel.legacyPackages.${system}
        else
          nixpkgs.legacyPackages.${system};

      forEachSystem = nixpkgs.lib.genAttrs [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
    in
    {
      packages = forEachSystem (system: rec {
        default = pulso;
        pulso = (pkgsFor system).callPackage ./nix/package.nix {
          inherit version;
          src = self;
        };
      });

      devShells = forEachSystem (system: {
        default = (pkgsFor system).mkShell {
          packages = with (pkgsFor system); [
            go
            gopls
            gotools
            golangci-lint
            goreleaser
          ];

          shellHook = ''
            echo "Pulso dev shell"
            echo "  Go:    $(go version)"
            echo "  Lint:  $(golangci-lint version --short 2>/dev/null || echo 'n/a')"
            echo ""
            echo "  make build / just build  — build pulso"
            echo "  make test  / just test   — run tests"
            echo "  make lint  / just lint   — run linter"
            echo "  make vet   / just vet    — run go vet"
          '';
        };
      });

      overlays.default = final: _prev: {
        pulso = self.packages.${final.stdenv.hostPlatform.system}.default;
      };

      formatter = forEachSystem (system: (pkgsFor system).nixfmt-tree);

      nixosModules.default = import ./nix/nixos-module.nix self;
      homeManagerModules.default = import ./nix/home-manager.nix self;
    };
}
