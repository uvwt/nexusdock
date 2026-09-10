import fs from 'node:fs';
import path from 'node:path';
import ts from 'typescript';

const webRoot = path.resolve(import.meta.dirname, '..');
const sourceRoot = path.join(webRoot, 'src');
const localePath = path.join(sourceRoot, 'locales', 'zh-CN.ts');
const localeDir = path.join(sourceRoot, 'locales', 'zh-CN');
const cjkPattern = /[\u3400-\u9fff\uf900-\ufaff]/u;

function sourceFiles(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) return entry.name === 'locales' ? [] : sourceFiles(fullPath);
    return /\.(?:ts|tsx)$/u.test(entry.name) ? [fullPath] : [];
  });
}

function parse(filePath) {
  return ts.createSourceFile(
    filePath,
    fs.readFileSync(filePath, 'utf8'),
    ts.ScriptTarget.Latest,
    true,
    filePath.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
  );
}

function lineOf(source, node) {
  return source.getLineAndCharacterOfPosition(node.getStart(source)).line + 1;
}

function translationKeys() {
  const keys = new Set();
  const duplicates = [];

  for (const filePath of [localePath, ...sourceFiles(localeDir)]) {
    const source = parse(filePath);
    function visit(node) {
      if (ts.isPropertyAssignment(node)) {
        const name = node.name;
        const key = ts.isStringLiteralLike(name) ? name.text : ts.isIdentifier(name) ? name.text : null;
        if (key) {
          if (keys.has(key)) duplicates.push(`${path.relative(webRoot, filePath)}:${lineOf(source, node)} duplicate key ${JSON.stringify(key)}`);
          keys.add(key);
        }
      }
      ts.forEachChild(node, visit);
    }
    visit(source);
  }
  return { keys, duplicates };
}

function isTranslationCall(node) {
  if (!ts.isCallExpression(node)) return false;
  if (ts.isIdentifier(node.expression)) return node.expression.text === 't';
  return ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === 't';
}

const { keys: zhKeys, duplicates } = translationKeys();
const missing = [];
const hardCoded = [];

for (const filePath of sourceFiles(sourceRoot)) {
  const source = parse(filePath);
  const relative = path.relative(webRoot, filePath);

  function visit(node) {
    if (isTranslationCall(node) && node.arguments.length > 0 && ts.isStringLiteralLike(node.arguments[0])) {
      const key = node.arguments[0].text;
      if (!zhKeys.has(key)) missing.push(`${relative}:${lineOf(source, node.arguments[0])} missing zh-CN translation for ${JSON.stringify(key)}`);
    }

    if ((ts.isStringLiteralLike(node) || ts.isJsxText(node) || ts.isTemplateLiteralToken(node))
      && cjkPattern.test(node.text)) {
      hardCoded.push(`${relative}:${lineOf(source, node)} hard-coded CJK text ${JSON.stringify(node.text.trim())}`);
    }
    ts.forEachChild(node, visit);
  }

  visit(source);
}

const errors = [...duplicates, ...missing, ...hardCoded];
if (errors.length > 0) {
  console.error(`i18n check failed with ${errors.length} issue(s):`);
  for (const error of errors) console.error(`- ${error}`);
  process.exit(1);
}

console.log(`i18n check passed: ${zhKeys.size} zh-CN translations, no hard-coded CJK UI text.`);
