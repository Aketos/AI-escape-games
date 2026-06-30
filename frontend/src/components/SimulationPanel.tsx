import { useState, useRef, useEffect } from 'react';
import { Send, Loader2, RotateCcw, MessageSquare } from 'lucide-react';
import type { GameState } from '../hooks/useGameState';
import { GamePanel, InventoryPanel } from './GamePanel';
import { roomImage } from '../utils/imagePath';

interface SimulationPanelProps {
  messages: { role: 'user' | 'ai'; content: string }[];
  gameState: GameState | null;
  loading: boolean;
  error: string | null;
  scenario: string | null;
  onSend: (message: string) => void;
  onReset: () => void;
}

export function SimulationPanel({
  messages, gameState, loading, error, scenario, onSend, onReset,
}: SimulationPanelProps) {
  const [input, setInput] = useState('');
  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [messages, loading]);

  // Keep focus in the input field after sending
  useEffect(() => {
    if (!loading) {
      inputRef.current?.focus();
    }
  }, [loading]);

  const handleSend = () => {
    if (!input.trim() || loading) return;
    onSend(input.trim());
    setInput('');
    inputRef.current?.focus();
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const handleItemClick = (itemName: string) => {
    setInput(prev => {
      const trimmed = prev.trim();
      if (trimmed === '') {
        return `J'examine ${itemName}`;
      }
      // If the input already ends with a partial command, append the item
      return `${trimmed} ${itemName}`;
    });
    inputRef.current?.focus();
  };

  const handleInventoryItemClick = (itemName: string) => {
    setInput(prev => {
      const trimmed = prev.trim();
      if (trimmed === '') {
        return `J'utilise ${itemName}`;
      }
      return `${trimmed} ${itemName}`;
    });
    inputRef.current?.focus();
  };

  const handleRoomClick = (roomName: string) => {
    const cmd = `Je vais dans ${roomName}`;
    setInput('');
    onSend(cmd);
  };

  const currentRoom = gameState?.rooms.find(r => r.current);
  const bgImage = roomImage(scenario, currentRoom?.image);

  return (
    <div className="min-h-screen relative flex flex-col">
      {/* Room background image */}
      {bgImage && (
        <div
          className="absolute inset-0 z-0 bg-cover bg-center transition-all duration-1000"
          style={{ backgroundImage: `url(${bgImage})` }}
        />
      )}
      <div className="scanlines"></div>

      {/* Header */}
      <div className="absolute top-8 left-8 text-[#00ffcc] font-mono text-sm tracking-widest z-20">
        BUNKER_OS_V1.4.2 <br/>
        STATUS: SIMULATION MODE
      </div>

      {/* Rooms Panel - Left Side */}
      <div className="absolute top-8 left-8 w-72 max-h-[calc(100vh-4rem)] overflow-y-auto bg-black/60 border border-gray-800 p-4 rounded z-20 backdrop-blur-sm" style={{ marginTop: '3.5rem' }}>
        <GamePanel state={gameState} scenario={scenario} onItemClick={handleItemClick} onRoomClick={handleRoomClick} />
      </div>

      {/* Inventory Panel - Right Side */}
      <div className="absolute top-8 right-8 w-64 max-h-[calc(100vh-4rem)] overflow-y-auto bg-black/60 border border-gray-800 p-4 rounded z-20 backdrop-blur-sm">
        <InventoryPanel state={gameState} scenario={scenario} onItemClick={handleInventoryItemClick} />
      </div>

      {/* Chat Panel - Center Bottom */}
      <div className="absolute bottom-0 left-1/2 -translate-x-1/2 w-full max-w-2xl z-20 flex flex-col" style={{ height: '60vh' }}>
        <div className="flex items-center justify-between bg-black/70 border border-gray-800 px-4 py-2 rounded-t backdrop-blur-sm">
          <div className="flex items-center gap-2 text-[#00ffcc]">
            <MessageSquare className="w-4 h-4" />
            <span className="text-xs font-mono uppercase tracking-wider">CHRONOS Simulation</span>
          </div>
          <button
            onClick={onReset}
            className="text-gray-500 hover:text-[#00ffcc] transition-colors"
            title="Reset conversation"
          >
            <RotateCcw className="w-4 h-4" />
          </button>
        </div>

        <div
          ref={scrollRef}
          className="flex-1 overflow-y-auto bg-black/70 border-x border-gray-800 p-4 backdrop-blur-sm flex flex-col gap-3"
        >
          {messages.length === 0 && (
            <div className="text-gray-600 italic text-sm font-mono text-center mt-8">
              Décrivez votre action à CHRONOS. Ex: "J'examine le sol" ou "J'utilise la fiole sur le mur".
            </div>
          )}
          {messages.map((msg, i) => (
            <div
              key={i}
              className={`max-w-[85%] p-3 rounded text-sm font-mono ${
                msg.role === 'user'
                  ? 'self-end bg-[#00ffcc]/10 border border-[#00ffcc]/30 text-[#00ffcc]'
                  : 'self-start bg-gray-900/80 border border-gray-700 text-gray-300'
              }`}
            >
              <div className="text-xs opacity-50 mb-1">
                {msg.role === 'user' ? 'JOUEUR' : 'CHRONOS'}
              </div>
              {msg.content}
            </div>
          ))}
          {loading && (
            <div className="self-start bg-gray-900/80 border border-gray-700 p-3 rounded text-sm font-mono text-gray-400 flex items-center gap-2">
              <Loader2 className="w-4 h-4 animate-spin" />
              <span>CHRONOS traite votre demande...</span>
            </div>
          )}
          {error && (
            <div className="self-center text-red-400 text-xs font-mono border border-red-500/30 bg-red-500/10 px-3 py-1 rounded">
              {error}
            </div>
          )}
        </div>

        <div className="flex gap-2 bg-black/70 border border-gray-800 p-3 rounded-b backdrop-blur-sm">
          <input
            ref={inputRef}
            type="text"
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            disabled={loading}
            placeholder="Tapez votre action..."
            className="flex-1 bg-black/50 border border-gray-700 text-gray-200 font-mono text-sm px-3 py-2 rounded focus:outline-none focus:border-[#00ffcc] disabled:opacity-50"
          />
          <button
            onClick={handleSend}
            disabled={loading || !input.trim()}
            className="px-4 py-2 bg-transparent border border-[#00ffcc] text-[#00ffcc] rounded hover:bg-[#00ffcc] hover:text-black transition-all disabled:opacity-30 disabled:cursor-not-allowed"
          >
            <Send className="w-4 h-4" />
          </button>
        </div>
      </div>
    </div>
  );
}
