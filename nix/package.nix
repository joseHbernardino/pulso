{
  lib,
  buildGoModule,
  python3,
  src,
  version ? "dev",
}:
buildGoModule {
  pname = "pulso";
  inherit version src;

  vendorHash = "sha256-K0mBtabgN3IgskAuONULxXxI/mvVC4KJo/PrmmHKab4=";

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

  # O teste de inspeção de projetos resolve o interpretador no PATH para provar
  # que uma sugestão Python é preparável. Sem python3 na fase de testes ele
  # falharia pelo ambiente, não pelo código.
  nativeCheckInputs = [ python3 ];

  meta = {
    description = "A modern process manager and application supervisor";
    license = lib.licenses.mit;
    mainProgram = "pulso";
    maintainers = [ ];
    platforms = lib.platforms.linux ++ lib.platforms.darwin;
  };
}
