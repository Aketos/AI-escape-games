export function roomImage(scenario: string | null, filename: string | undefined): string | undefined {
  if (!filename) return undefined;
  return `/images/${scenario}/rooms/${filename}`;
}

export function itemImage(scenario: string | null, filename: string | undefined): string | undefined {
  if (!filename) return undefined;
  return `/images/${scenario}/items/${filename}`;
}
