export function scenarioImage(scenario: string | null, filename: string | undefined): string | undefined {
  if (!filename) return undefined;
  return `/images/${scenario}/${filename}`;
}
