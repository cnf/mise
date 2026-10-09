{ pkgs, ... }:

{
  languages.go.enable = true;

  packages = with pkgs; [
    sqlite
    git
  ];

  env.PARTDB_APIKEY_FILE = "partdb_apikey";
  env.PARTDB_BASE_URL = "https://parts.home.nullsense.net/api";
}
