import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { generateApi } from 'swagger-typescript-api';

const scriptDir = dirname(fileURLToPath(import.meta.url));
const webDir = resolve(scriptDir, '..');
const repoDir = resolve(webDir, '..');
const contractScript = join(repoDir, 'scripts', 'generate-contracts.py');
const outputPath = join(webDir, 'src', 'api', 'generated.ts');
const checkOnly = process.argv.includes('--check');

const canonicalOpenAPI = execFileSync('python3', [contractScript], {
  cwd: repoDir,
  encoding: 'utf8',
}).trim();
const openapi = JSON.parse(canonicalOpenAPI);
const contractHash = createHash('sha256').update(canonicalOpenAPI).digest('hex');

const result = await generateApi({
  spec: openapi,
  output: false,
  generateClient: false,
  enumStyle: 'union',
  sortTypes: true,
  fileName: 'generated.ts',
  silent: true,
});

if (result.files.length !== 1 || result.files[0].fileExtension !== '.ts') {
  throw new Error(`expected one generated TypeScript contract file, got ${result.files.length}`);
}

const generated = [
  '// Code generated from scripts/generate-contracts.py by swagger-typescript-api. DO NOT EDIT.',
  `// OpenAPI-SHA256: ${contractHash}`,
  '',
  result.files[0].fileContent.trimEnd(),
  '',
].join('\n');

if (checkOnly) {
  let current = '';
  try {
    current = readFileSync(outputPath, 'utf8');
  } catch {
    // Missing generated output is reported as drift below.
  }
  if (current !== generated) {
    console.error('generated API types are stale; run: npm run generate:api-types');
    process.exit(1);
  }
  console.log('generated API types are current.');
} else {
  writeFileSync(outputPath, generated);
  console.log(`generated ${outputPath.replace(repoDir + '/', '')}`);
}
