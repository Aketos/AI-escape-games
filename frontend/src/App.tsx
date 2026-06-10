import { useAudioStream } from './hooks/useAudioStream';
import { AudioVisualizer } from './components/AudioVisualizer';
import { Mic, Terminal, Loader2 } from 'lucide-react';

function App() {
  const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const wsUrl = import.meta.env.VITE_WS_URL || `${wsProtocol}//${window.location.host}/ws`;
  const { status, connect, disconnect, micVolume, aiVolume, logs } = useAudioStream(wsUrl);

  return (
    <div className="min-h-screen relative flex flex-col items-center justify-center">
      <div className="scanlines"></div>

      {/* Header */}
      <div className="absolute top-8 left-8 text-[#00ffcc] font-mono text-sm tracking-widest">
        BUNKER_OS_V1.4.2 <br/>
        STATUS: {status.toUpperCase()}
      </div>

      {/* Main Visualizer */}
      <div className="z-20 mb-16">
        <AudioVisualizer micVolume={micVolume} aiVolume={aiVolume} />
      </div>

      {/* Action Button */}
      <div className="z-20">
        {status === 'Idle' || status === 'Disconnected' ? (
          <button 
            onClick={connect}
            className="px-8 py-4 bg-transparent border-2 border-[#00ffcc] text-[#00ffcc] font-mono font-bold tracking-widest uppercase hover:bg-[#00ffcc] hover:text-[#0f0f11] transition-all duration-300 shadow-[0_0_15px_rgba(0,255,204,0.5)]"
          >
            Connect to Bunker
          </button>
        ) : status === 'Booting' ? (
          <button 
            disabled
            className="px-8 py-4 bg-transparent border-2 border-yellow-500 text-yellow-500 font-mono font-bold tracking-widest uppercase flex items-center gap-3"
          >
            <Loader2 className="w-5 h-5 animate-spin" />
            Booting GPU...
          </button>
        ) : (
          <button 
            onPointerDown={() => { /* Implement push-to-mute if needed */ }}
            onPointerUp={() => { /* Implement push-to-mute if needed */ }}
            onClick={disconnect} // Using click to disconnect for now
            className="px-8 py-4 bg-transparent border-2 border-[#ff3333] text-[#ff3333] font-mono font-bold tracking-widest uppercase hover:bg-[#ff3333] hover:text-[#0f0f11] transition-all duration-300 shadow-[0_0_15px_rgba(255,51,51,0.5)] flex items-center gap-3"
          >
            <Mic className="w-5 h-5" />
            Disconnect
          </button>
        )}
      </div>

      {/* Terminal UI */}
      <div className="absolute bottom-8 left-1/2 -translate-x-1/2 w-full max-w-2xl bg-black/60 border border-gray-800 p-4 rounded z-20 backdrop-blur-sm">
        <div className="flex items-center gap-2 text-gray-500 mb-2 border-b border-gray-800 pb-2">
          <Terminal className="w-4 h-4" />
          <span className="text-xs font-mono uppercase tracking-wider">System Logs</span>
        </div>
        <div className="h-32 overflow-y-auto font-mono text-sm text-[#00ffcc] flex flex-col gap-1">
          {logs.map((log, i) => (
            <div key={i} className="opacity-80 hover:opacity-100">{log}</div>
          ))}
          {logs.length === 0 && <div className="text-gray-600 italic">Awaiting connection...</div>}
        </div>
      </div>
    </div>
  );
}

export default App;
