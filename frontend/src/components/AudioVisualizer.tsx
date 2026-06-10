import { motion } from 'framer-motion';

interface Props {
  micVolume: number; // 0 to 1
  aiVolume: number;  // 0 to 1
}

export function AudioVisualizer({ micVolume, aiVolume }: Props) {
  // Base scale is 1. We scale up based on volume (up to 1.5x)
  const micScale = 1 + micVolume * 2; 
  const aiScale = 1 + aiVolume * 2;

  return (
    <div className="relative w-64 h-64 flex items-center justify-center">
      {/* AI Ring (Purple/Red) */}
      <motion.div
        className="absolute inset-0 rounded-full border-4 border-[#ff00ff]"
        animate={{
          scale: aiScale,
          opacity: aiVolume > 0.01 ? 0.8 : 0.2,
          boxShadow: aiVolume > 0.01 ? `0 0 ${20 * aiScale}px #ff00ff, inset 0 0 ${20 * aiScale}px #ff00ff` : 'none',
        }}
        transition={{ type: 'spring', stiffness: 300, damping: 20 }}
      />
      
      {/* Mic Ring (Green/Cyan) */}
      <motion.div
        className="absolute inset-4 rounded-full border-4 border-[#00ffcc]"
        animate={{
          scale: micScale,
          opacity: micVolume > 0.01 ? 0.8 : 0.2,
          boxShadow: micVolume > 0.01 ? `0 0 ${20 * micScale}px #00ffcc, inset 0 0 ${20 * micScale}px #00ffcc` : 'none',
        }}
        transition={{ type: 'spring', stiffness: 300, damping: 20 }}
      />
      
      {/* Center Core */}
      <div className="absolute inset-12 rounded-full bg-cyber-dark border border-gray-800 flex items-center justify-center">
        <span className="text-gray-500 text-xs tracking-widest font-bold">A I . S 2 S</span>
      </div>
    </div>
  );
}
