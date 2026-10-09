{
  description = "mise — BOM and assembly-manual tool";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forAll = f: nixpkgs.lib.genAttrs systems (s: f nixpkgs.legacyPackages.${s});
    in
    {
      packages = forAll (pkgs: rec {
        mise = pkgs.buildGoModule {
          pname = "mise";
          version = "0.1.0";
          # An allow-list, so a stray database or the Part-DB key in the working
          # tree can never be copied into the (world-readable) Nix store.
          src = pkgs.lib.fileset.toSource {
            root = ./.;
            fileset = pkgs.lib.fileset.unions [
              ./go.mod
              ./go.sum
              ./cmd
              ./internal
            ];
          };
          subPackages = [ "cmd/mise" ];
          vendorHash = "sha256-7IC/p5GlD2EZkDXQzkaZ7E19S/ABKEBsg68vt8pykis=";
          env.CGO_ENABLED = "0"; # modernc.org/sqlite is pure Go
          meta.mainProgram = "mise";
        };
        default = mise;
      });

      apps = forAll (pkgs: rec {
        mise = { type = "app"; program = "${self.packages.${pkgs.system}.mise}/bin/mise"; };
        default = mise;
      });

      devShells = forAll (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [ go gotools sqlite git ];
        };
      });
    };
}
