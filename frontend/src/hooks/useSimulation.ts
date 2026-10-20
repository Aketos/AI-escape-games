import { useState, useCallback } from 'react';
import type { GameState } from './useGameState';
import { playSfx } from '../utils/sfx';

interface SimulationMessage {
  role: 'user' | 'ai';
  content: string;
}

interface UseSimulationReturn {
  messages: SimulationMessage[];
  gameState: GameState | null;
  loading: boolean;
  error: string | null;
  sendMessage: (message: string, scenario: string) => void;
  init: (scenario: string) => void;
  reset: () => void;
}

interface SimulationResponse {
  narration?: string;
  game_state?: GameState;
  sfx?: string;
}

// postSimulation calls /api/simulate. token is the server's LLM_PROXY_TOKEN
// (empty when the server runs without one, e.g. local dev).
async function postSimulation(body: object, token: string): Promise<SimulationResponse> {
  const baseUrl = import.meta.env.VITE_API_URL || '';
  const headers: Record<string, string> = { 'Content-Type': 'application/json' };
  if (token) headers.Authorization = `Bearer ${token}`;
  const res = await fetch(`${baseUrl}/api/simulate`, { method: 'POST', headers, body: JSON.stringify(body) });
  if (!res.ok) {
    throw new Error(`Server error: ${res.status} ${await res.text()}`);
  }
  return res.json();
}

export function useSimulation(token: string): UseSimulationReturn {
  const [messages, setMessages] = useState<SimulationMessage[]>([]);
  const [gameState, setGameState] = useState<GameState | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // applyResponse updates game state and SFX, and returns the narration.
  const applyResponse = useCallback((data: SimulationResponse, scenario: string) => {
    if (data.game_state) setGameState(data.game_state);
    if (data.sfx) playSfx(scenario, data.sfx);
    return data.narration || '(no response)';
  }, []);

  const sendMessage = useCallback(async (message: string, scenario: string) => {
    if (!message.trim() || loading) return;

    setMessages((prev) => [...prev, { role: 'user', content: message }]);
    setLoading(true);
    setError(null);

    try {
      const narration = applyResponse(await postSimulation({ message, scenario }, token), scenario);
      setMessages((prev) => [...prev, { role: 'ai', content: narration }]);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      setMessages((prev) => [...prev, { role: 'ai', content: `[Error] ${msg}` }]);
    } finally {
      setLoading(false);
    }
  }, [loading, token, applyResponse]);

  const init = useCallback(async (scenario: string) => {
    setLoading(true);
    setError(null);
    try {
      const narration = applyResponse(await postSimulation({ scenario, init: true }, token), scenario);
      setMessages([{ role: 'ai', content: narration }]);
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
    } finally {
      setLoading(false);
    }
  }, [token, applyResponse]);

  const reset = useCallback(() => {
    setMessages([]);
    setGameState(null);
    setError(null);
  }, []);

  return { messages, gameState, loading, error, sendMessage, init, reset };
}
