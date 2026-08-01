import fs from 'node:fs/promises';
import path from 'node:path';

function slugify(value) {
  return (
    String(value)
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '') || 'run'
  );
}

export async function createRunDirectory(outDir, scenarioId) {
  const stamp = new Date().toISOString().replace(/[:.]/g, '-');
  const runDirPrefix = path.join(outDir, `${stamp}-${slugify(scenarioId)}-`);
  const runDir = await fs.mkdtemp(runDirPrefix);
  const screenshotsDir = path.join(runDir, 'screenshots');

  await fs.mkdir(screenshotsDir, { recursive: true });

  return { runDir, screenshotsDir };
}

export async function writeJsonArtifact(filePath, value) {
  await fs.writeFile(filePath, `${JSON.stringify(value, null, 2)}\n`, 'utf8');
}

export async function writeRunArtifacts({ outDir, capture, plan }) {
  const { runDir, screenshotsDir } = await createRunDirectory(outDir, plan.scenarioId);

  await writeJsonArtifact(path.join(runDir, 'capture.json'), capture);
  await writeJsonArtifact(path.join(runDir, 'plan.json'), plan);

  return { runDir, screenshotsDir };
}
