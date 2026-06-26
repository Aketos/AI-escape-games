import { useState, useCallback } from 'react';

export interface GameItem {
  id: string;
  name: string;
  type: string;
  visible: boolean;
  inspected: boolean;
  state?: string;
  contains?: string[];
}

export interface GameRoom {
  id: string;
  name: string;
  description: string;
  current: boolean;
  visited: boolean;
  items: GameItem[];
}

export interface InventoryItem {
  id: string;
  name: string;
  state?: string;
}

export interface GameState {
  rooms: GameRoom[];
  inventory: InventoryItem[];
  oxygen: number;
  won: boolean;
  history: string[];
}

interface UseGameStateReturn {
  state: GameState | null;
  refresh: () => void;
  clearState: () => void;
  handleGameStateMessage: (data: unknown) => void;
  handleIntroComplete: () => void;
}

export function useGameState(): UseGameStateReturn {
  const [state, setState] = useState<GameState | null>(null);

  const fetchState = useCallback(async () => {
    try {
      const baseUrl = import.meta.env.VITE_API_URL || '';
      const res = await fetch(`${baseUrl}/game-state`);
      if (res.ok) {
        const data = await res.json();
        setState(data);
      }
    } catch (e) {
      console.error('Failed to fetch game state:', e);
    }
  }, []);

  const handleGameStateMessage = useCallback((data: unknown) => {
    setState(data as GameState);
  }, []);

  const clearState = useCallback(() => {
    setState(null);
  }, []);

  const handleIntroComplete = useCallback(() => {
    fetchState();
  }, [fetchState]);

  return { state, refresh: fetchState, clearState, handleGameStateMessage, handleIntroComplete };
}
