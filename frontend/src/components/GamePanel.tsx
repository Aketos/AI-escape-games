import { useState } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import { MapPin, Backpack, Eye, Search, ChevronRight, Package, Clock, Trophy, CheckCircle2 } from 'lucide-react';
import type { GameState, GameRoom, GameItem } from '../hooks/useGameState';
import { itemImage } from '../utils/imagePath';

interface Props {
  state: GameState | null;
  scenario?: string | null;
}

export function GamePanel({ state }: Props) {
  const [selectedRoomId, setSelectedRoomId] = useState<string | null>(null);

  if (!state) {
    return (
      <div className="text-gray-600 font-mono text-xs italic">
        Awaiting game data...
      </div>
    );
  }

  const currentRoom = state.rooms.find(r => r.current);
  const selectedRoom = state.rooms.find(r => r.id === selectedRoomId) || currentRoom;

  return (
    <div className="flex flex-col gap-4 h-full">
      {/* Oxygen & Victory */}
      <div className="flex items-center gap-3">
        <div className={`flex items-center gap-2 px-3 py-1.5 border rounded font-mono text-xs tracking-wider ${
          state.oxygen <= 10
            ? 'border-[#ff3333] text-[#ff3333] animate-pulse'
            : state.oxygen <= 20
            ? 'border-yellow-500 text-yellow-500'
            : 'border-[#00ffcc] text-[#00ffcc]'
        }`}>
          <Clock className="w-3.5 h-3.5" />
          O₂: {state.oxygen}min
        </div>
        {state.won && (
          <div className="flex items-center gap-2 px-3 py-1.5 border border-[#ff00ff] text-[#ff00ff] rounded font-mono text-xs tracking-wider">
            <Trophy className="w-3.5 h-3.5" />
            ESCAPED
          </div>
        )}
      </div>

      {/* Rooms Map */}
      <div>
        <div className="flex items-center gap-2 text-gray-500 mb-2 border-b border-gray-800 pb-1.5">
          <MapPin className="w-3.5 h-3.5" />
          <span className="text-xs font-mono uppercase tracking-wider">Rooms</span>
        </div>
        <div className="flex flex-col gap-1.5">
          {state.rooms.map(room => (
            <RoomButton
              key={room.id}
              room={room}
              selected={selectedRoom?.id === room.id}
              onClick={() => setSelectedRoomId(room.id)}
            />
          ))}
        </div>
      </div>

      {/* Selected Room Detail */}
      {selectedRoom && (
        <RoomDetail room={selectedRoom} />
      )}
    </div>
  );
}

export function InventoryPanel({ state, scenario }: Props) {
  if (!state) {
    return (
      <div className="text-gray-600 font-mono text-xs italic">
        Awaiting game data...
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-4 h-full">
      {/* Oxygen & Victory */}
      <div className="flex items-center gap-3">
        <div className={`flex items-center gap-2 px-3 py-1.5 border rounded font-mono text-xs tracking-wider ${
          state.oxygen <= 10
            ? 'border-[#ff3333] text-[#ff3333] animate-pulse'
            : state.oxygen <= 20
            ? 'border-yellow-500 text-yellow-500'
            : 'border-[#00ffcc] text-[#00ffcc]'
        }`}>
          <Clock className="w-3.5 h-3.5" />
          O₂: {state.oxygen}min
        </div>
        {state.won && (
          <div className="flex items-center gap-2 px-3 py-1.5 border border-[#ff00ff] text-[#ff00ff] rounded font-mono text-xs tracking-wider">
            <Trophy className="w-3.5 h-3.5" />
            ESCAPED
          </div>
        )}
      </div>

      {/* Inventory */}
      <div>
        <div className="flex items-center gap-2 text-gray-500 mb-2 border-b border-gray-800 pb-1.5">
          <Backpack className="w-3.5 h-3.5" />
          <span className="text-xs font-mono uppercase tracking-wider">Inventory</span>
        </div>
        {state.inventory.length === 0 ? (
          <div className="text-gray-600 font-mono text-xs italic">Empty</div>
        ) : (
          <div className="flex flex-col gap-1.5">
            {state.inventory.map(item => {
              const img = itemImage(scenario ?? null, item.image);
              return (
              <div
                key={item.id}
                className="flex items-center gap-1.5 px-2 py-1.5 bg-[#00ffcc]/5 border border-[#00ffcc]/30 rounded font-mono text-xs text-[#00ffcc]"
              >
                {img ? (
                  <img src={img} alt={item.name} className="w-8 h-8 rounded object-cover flex-shrink-0" />
                ) : (
                  <Package className="w-3 h-3" />
                )}
                {item.name}
                {item.state && (
                  <span className="ml-auto text-[10px] text-yellow-500/70 uppercase">{item.state}</span>
                )}
              </div>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}

function RoomButton({ room, selected, onClick }: { room: GameRoom; selected: boolean; onClick: () => void }) {
  if (!room.visited) {
    return (
      <div className="flex items-center gap-2 px-3 py-2 border border-gray-800/50 rounded font-mono text-xs text-gray-700 italic">
        <div className="w-1.5 h-1.5 rounded-full bg-gray-800" />
        ??? — undiscovered
      </div>
    );
  }

  return (
    <button
      onClick={onClick}
      className={`flex items-center gap-2 px-3 py-2 border rounded font-mono text-xs tracking-wide transition-all duration-200 ${
        selected
          ? 'border-[#00ffcc] text-[#00ffcc] bg-[#00ffcc]/5 shadow-[0_0_10px_rgba(0,255,204,0.2)]'
          : room.current
          ? 'border-[#00ffcc]/40 text-[#00ffcc]/80 hover:border-[#00ffcc]/70'
          : 'border-gray-800 text-gray-400 hover:border-gray-700 hover:text-gray-300'
      }`}
    >
      <div className={`w-1.5 h-1.5 rounded-full ${room.current ? 'bg-[#00ffcc] animate-pulse' : 'bg-gray-600'}`} />
      {room.name}
      {room.current && (
        <span className="ml-auto text-[#00ffcc] text-[10px] uppercase">Here</span>
      )}
    </button>
  );
}

function RoomDetail({ room }: { room: GameRoom }) {
  const itemMap = new Map<string, GameItem>();
  room.items.forEach(i => itemMap.set(i.id, i));

  // Find which items are children of another item
  const childIds = new Set<string>();
  room.items.forEach(item => {
    if (item.contains) {
      item.contains.forEach(cid => childIds.add(cid));
    }
  });

  // Top-level items: visible items that are not children of any other item
  const topLevel = room.items.filter(i => i.visible && !childIds.has(i.id));
  const hiddenCount = room.items.filter(i => !i.visible).length;

  return (
    <motion.div
      key={room.id}
      initial={{ opacity: 0, y: 5 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.2 }}
      className="border border-gray-800 rounded p-3 bg-black/40"
    >
      <div className="text-[#00ffcc] font-mono text-sm font-bold mb-1">{room.name}</div>
      {room.description && (
        <div className="text-gray-500 font-mono text-xs mb-3 leading-relaxed">{room.description}</div>
      )}

      <div className="flex items-center gap-1.5 text-gray-500 mb-2">
        <Search className="w-3 h-3" />
        <span className="text-[10px] font-mono uppercase tracking-wider">Visible Elements</span>
      </div>

      <div className="flex flex-col gap-1">
        <AnimatePresence mode="popLayout">
          {topLevel.map(item => (
            <ItemTree key={item.id} item={item} itemMap={itemMap} depth={0} />
          ))}
        </AnimatePresence>
      </div>

      {hiddenCount > 0 && (
        <div className="text-gray-700 font-mono text-[10px] italic mt-2">
          + {hiddenCount} hidden element{hiddenCount > 1 ? 's' : ''}
        </div>
      )}
    </motion.div>
  );
}

function ItemTree({ item, itemMap, depth }: { item: GameItem; itemMap: Map<string, GameItem>; depth: number }) {
  const children: GameItem[] = (item.contains || [])
    .map(cid => itemMap.get(cid))
    .filter((c): c is GameItem => c !== undefined && c.visible);

  return (
    <div className="flex flex-col gap-1">
      <ItemRow item={item} depth={depth} />
      {children.map(child => (
        <ItemTree key={child.id} item={child} itemMap={itemMap} depth={depth + 1} />
      ))}
    </div>
  );
}

function ItemRow({ item, depth }: { item: GameItem; depth: number }) {
  const isZone = item.type === 'zone';
  const inspected = item.inspected;

  const baseClasses = isZone
    ? inspected
      ? 'bg-[#ff00ff]/5 border border-[#00ffcc]/40 text-[#ff00ff]/50'
      : 'bg-[#ff00ff]/5 border border-[#ff00ff]/20 text-[#ff00ff]/80'
    : inspected
      ? 'bg-gray-900/30 border border-[#00ffcc]/30 text-gray-500'
      : 'bg-gray-900/50 border border-gray-800 text-gray-400';

  return (
    <div
      style={{ paddingLeft: `${0.5 + depth * 1.2}rem` }}
      className={`flex items-center gap-2 px-2 py-1.5 rounded font-mono text-xs transition-all ${baseClasses} ${
        inspected ? 'opacity-60' : 'opacity-100'
      }`}
    >
      {inspected ? (
        <CheckCircle2 className="w-3 h-3 flex-shrink-0 text-[#00ffcc]/70" />
      ) : isZone ? (
        <Eye className="w-3 h-3 flex-shrink-0" />
      ) : (
        <ChevronRight className="w-3 h-3 flex-shrink-0" />
      )}
      <span className={`truncate ${inspected ? 'line-through decoration-[#00ffcc]/30' : ''}`}>{item.name}</span>
      {inspected && (
        <span className="ml-auto text-[10px] text-[#00ffcc]/50 uppercase">Done</span>
      )}
      {item.state && item.state !== 'dirty' && (
        <span className={`ml-auto text-[10px] uppercase ${inspected ? 'text-yellow-500/50' : 'text-yellow-500/70'}`}>{item.state}</span>
      )}
    </div>
  );
}
