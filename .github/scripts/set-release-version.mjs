import { readFile, writeFile } from "node:fs/promises";

const version = process.argv[2];

if (!/^0\.1\.\d+$/.test(version ?? "")) {
  throw new Error("release version must match 0.1.<github-run-number>");
}

const configPath = new URL("../../wails.json", import.meta.url);
const config = JSON.parse(await readFile(configPath, "utf8"));

config.info = {
  ...config.info,
  productVersion: version,
};

await writeFile(configPath, `${JSON.stringify(config, null, 2)}\n`);
