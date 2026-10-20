// playSfx plays a scenario sound effect from public/sounds/<scenario>/<name>.opus.
export function playSfx(scenario: string | null, name: string) {
  if (!name) return;
  const audio = new Audio(`/sounds/${scenario ? `${scenario}/` : ''}${name}.opus`);
  audio.volume = 0.85;
  audio.play().catch((e) => console.warn(`Could not play SFX ${name}.opus:`, e));
}
