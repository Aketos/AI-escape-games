import { useState, useCallback, useRef } from 'react';
import type { GameState } from './useGameState';

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

export function useSimulation(): UseSimulationReturn {
  const [messages, setMessages] = useState<SimulationMessage[]>([]);
  const [gameState, setGameState] = useState<GameState | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const initializedRef = useRef(false);

  const sendMessage = useCallback(async (message: string, scenario: string) => {
    if (!message.trim() || loading) return;

    setMessages((prev) => [...prev, { role: 'user', content: message }]);
    setLoading(true);
    setError(null);

    try {
      const baseUrl = import.meta.env.VITE_API_URL || '';
      const res = await fetch(`${baseUrl}/api/simulate`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message, scenario }),
      });

      if (!res.ok) {
        const errBody = await res.text();
        throw new Error(`Server error: ${res.status} ${errBody}`);
      }

      const data = await res.json();
      const narration: string = data.narration || '(pas de réponse)';
      setMessages((prev) => [...prev, { role: 'ai', content: narration }]);
      if (data.game_state) {
        setGameState(data.game_state as GameState);
      }
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
      setMessages((prev) => [...prev, { role: 'ai', content: `[Erreur] ${msg}` }]);
    } finally {
      setLoading(false);
    }
  }, [loading]);

  const init = useCallback(async (scenario: string) => {
    setLoading(true);
    setError(null);
    try {
      const baseUrl = import.meta.env.VITE_API_URL || '';
      const res = await fetch(`${baseUrl}/api/simulate`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ scenario, init: true }),
      });
      if (!res.ok) {
        const errBody = await res.text();
        throw new Error(`Server error: ${res.status} ${errBody}`);
      }
      const data = await res.json();
      const narration: string = data.narration || '(pas de réponse)';
      setMessages([{ role: 'ai', content: narration }]);
      if (data.game_state) {
        setGameState(data.game_state as GameState);
      }
      initializedRef.current = true;
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      setError(msg);
    } finally {
      setLoading(false);
    }
  }, []);

  const reset = useCallback(() => {
    setMessages([]);
    setGameState(null);
    setError(null);
    initializedRef.current = false;
  }, []);

  return { messages, gameState, loading, error, sendMessage, init, reset };
}
