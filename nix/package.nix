{
  lib,
  buildGoModule,
  src,
  version ? "dev",
}:
buildGoModule {
  pname = "pulso";
  inherit version src;

  vendorHash = "sha256-DMIGgbOGHNZWWUUCl6Jk1yt2Jm1iqsPzYpM7zWC1kj4=";

  proxyVendor = true;

  env.CGO_ENABLED = 0;

  ldflags = [
    "-s"
    "-w"
    "-X pulso.local/pulso/internal/version.Version=${version}"
    "-X pulso.local/pulso/internal/version.BuildTime=1970-01-01T00:00:00Z"
  ];

  # e2e tests need a running supervisor
  checkFlags = [ "-short" ];

  meta = {
    description = "A modern process manager and application supervisor";
    license = lib.licenses.mit;
    mainProgram = "pulso";
    maintainers = [ ];
    platforms = lib.platforms.linux ++ lib.platforms.darwin;
  };
}
