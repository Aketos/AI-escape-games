import { useState, useCallback } from 'react';

export interface GameItem {
  id: string;
  name: string;
  image?: string;
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
  image?: string;
  current: boolean;
  visited: boolean;
  items: GameItem[];
}

export interface InventoryItem {
  id: string;
  name: string;
  state?: string;
  image?: string;
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
  clearState: () => void;
  handleGameStateMessage: (data: unknown) => void;
}

export function useGameState(): UseGameStateReturn {
  const [state, setState] = useState<GameState | null>(null);

  const handleGameStateMessage = useCallback((data: unknown) => {
    setState(data as GameState);
  }, []);

  const clearState = useCallback(() => {
    setState(null);
  }, []);

  return { state, clearState, handleGameStateMessage };
}
