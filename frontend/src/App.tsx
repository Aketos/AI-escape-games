import { useEffect, useState } from 'react';
import { useAudioStream } from './hooks/useAudioStream';
import { useGameState } from './hooks/useGameState';
import { useSimulation } from './hooks/useSimulation';
import { AudioVisualizer } from './components/AudioVisualizer';
import { GamePanel, InventoryPanel } from './components/GamePanel';
import { SimulationPanel } from './components/SimulationPanel';
import { Mic, Terminal, Loader2, Map, ChevronUp, ChevronDown, FlaskConical } from 'lucide-react';
import { roomImage } from './utils/imagePath';

interface Scenario {
  id: string;
  name: string;
  description: string;
  difficulty: string;
}

function App() {
  const wsProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const wsUrl = import.meta.env.VITE_WS_URL || `${wsProtocol}//${window.location.host}/ws`;
  const { status, connect, disconnect, micVolume, aiVolume, logs, onGameState, onIntroComplete } = useAudioStream(wsUrl);
  const { state: gameState, clearState, handleGameStateMessage, handleIntroComplete } = useGameState();
  const sim = useSimulation();

  const [scenarios, setScenarios] = useState<Scenario[]>([]);
  const [selectedScenario, setSelectedScenario] = useState<string | null>(null);
  const [scenariosLoading, setScenariosLoading] = useState(true);
  const [logsExpanded, setLogsExpanded] = useState(false);
  const [simulationMode, setSimulationMode] = useState(false);
  const [simActive, setSimActive] = useState(false);

  useEffect(() => {
    fetch('/scenarios')
      .then((res) => res.json())
      .then((data: Scenario[]) => {
        setScenarios(data);
        if (data.length > 0) setSelectedScenario(data[0].id);
      })
      .catch((err) => console.error('Failed to load scenarios:', err))
      .finally(() => setScenariosLoading(false));
  }, []);

  useEffect(() => {
    onGameState(handleGameStateMessage);
  }, [onGameState, handleGameStateMessage]);

  useEffect(() => {
    onIntroComplete(handleIntroComplete);
  }, [onIntroComplete, handleIntroComplete]);

  const handleConnect = () => {
    if (!selectedScenario) return;
    if (simulationMode) {
      sim.reset();
      setSimActive(true);
      return;
    }
    clearState();
    connect(selectedScenario);
  };

  const handleDisconnect = () => {
    if (simActive) {
      setSimActive(false);
      sim.reset();
      return;
    }
    disconnect();
  };

  const handleSimSend = (message: string) => {
    if (selectedScenario) {
      sim.sendMessage(message, selectedScenario);
    }
  };

  const handleSimReset = () => {
    sim.reset();
  };

  const showScenarioPicker = (status === 'Idle' || status === 'Disconnected') && !scenariosLoading && !simActive;
  const isConnected = simActive || status === 'Connected' || status === 'Ready' || status === 'Booting';

  const currentRoom = (simActive ? sim.gameState : gameState)?.rooms.find(r => r.current);
  const bgImage = roomImage(selectedScenario, currentRoom?.image);

  if (simActive) {
    return (
      <SimulationPanel
        messages={sim.messages}
        gameState={sim.gameState}
        loading={sim.loading}
        error={sim.error}
        scenario={selectedScenario}
        onSend={handleSimSend}
        onReset={handleSimReset}
      />
    );
  }

  return (
    <div className="min-h-screen relative flex flex-col items-center justify-center">
      {/* Room background image */}
      {bgImage && (
        <div
          className="absolute inset-0 z-0 bg-cover bg-center transition-all duration-1000"
          style={{ backgroundImage: `url(${bgImage})` }}
        />
      )}
      <div className="scanlines"></div>

      {/* Header */}
      <div className="absolute top-8 left-8 text-[#00ffcc] font-mono text-sm tracking-widest">
        BUNKER_OS_V1.4.2 <br/>
        STATUS: {status.toUpperCase()}
      </div>

      {/* Rooms Panel - Left Side */}
      <div className="absolute top-8 left-8 w-72 max-h-[calc(100vh-4rem)] overflow-y-auto bg-black/60 border border-gray-800 p-4 rounded z-20 backdrop-blur-sm" style={{ marginTop: '3.5rem' }}>
        <GamePanel state={gameState} scenario={selectedScenario} />
      </div>

      {/* Inventory Panel - Right Side */}
      <div className="absolute top-8 right-8 w-64 max-h-[calc(100vh-4rem)] overflow-y-auto bg-black/60 border border-gray-800 p-4 rounded z-20 backdrop-blur-sm">
        <InventoryPanel state={gameState} scenario={selectedScenario} />
      </div>

      {/* Main Visualizer */}
      <div className="z-20 mb-16">
        <AudioVisualizer micVolume={micVolume} aiVolume={aiVolume} />
      </div>

      {/* Action Button + Scenario Picker */}
      <div className="z-20 flex flex-col items-center gap-4">
        {showScenarioPicker && scenarios.length > 0 && (
          <div className="bg-black/60 border border-gray-800 p-4 rounded backdrop-blur-sm w-full max-w-md">
            <div className="flex items-center gap-2 text-[#00ffcc] mb-3 border-b border-gray-800 pb-2">
              <Map className="w-4 h-4" />
              <span className="text-xs font-mono uppercase tracking-wider">Select Scenario</span>
            </div>
            <div className="flex flex-col gap-2">
              {scenarios.map((s) => (
                <button
                  key={s.id}
                  onClick={() => setSelectedScenario(s.id)}
                  className={`text-left p-3 rounded border transition-all duration-200 ${
                    selectedScenario === s.id
                      ? 'border-[#00ffcc] bg-[#00ffcc]/10 text-[#00ffcc]'
                      : 'border-gray-800 text-gray-400 hover:border-gray-600'
                  }`}
                >
                  <div className="flex items-center justify-between">
                    <div className="font-mono font-bold text-sm">{s.name}</div>
                    {s.difficulty && (
                      <span className={`text-xs font-mono px-2 py-0.5 rounded ${
                        s.difficulty === 'hard' ? 'bg-red-500/20 text-red-400' :
                        s.difficulty === 'medium' ? 'bg-yellow-500/20 text-yellow-400' :
                        'bg-green-500/20 text-green-400'
                      }`}>{s.difficulty}</span>
                    )}
                  </div>
                  {s.description && (
                    <div className="text-xs text-gray-500 mt-1">{s.description}</div>
                  )}
                </button>
              ))}
            </div>

            {/* Simulation mode toggle */}
            <label className="flex items-center gap-2 mt-3 pt-3 border-t border-gray-800 cursor-pointer group">
              <div className="relative">
                <input
                  type="checkbox"
                  checked={simulationMode}
                  onChange={(e) => setSimulationMode(e.target.checked)}
                  className="sr-only peer"
                />
                <div className="w-9 h-5 bg-gray-700 rounded-full peer-checked:bg-[#00ffcc]/40 transition-colors"></div>
                <div className="absolute top-0.5 left-0.5 w-4 h-4 bg-gray-400 rounded-full peer-checked:bg-[#00ffcc] peer-checked:translate-x-4 transition-all"></div>
              </div>
              <div className="flex items-center gap-1.5 text-xs font-mono text-gray-500 group-hover:text-[#00ffcc] transition-colors">
                <FlaskConical className="w-3.5 h-3.5" />
                <span>Mode Simulation (sans Unmute)</span>
              </div>
            </label>
          </div>
        )}

        {showScenarioPicker && scenariosLoading && (
          <div className="text-gray-500 font-mono text-sm flex items-center gap-2">
            <Loader2 className="w-4 h-4 animate-spin" />
            Loading scenarios...
          </div>
        )}

        {!isConnected ? (
          <button 
            onClick={handleConnect}
            disabled={!selectedScenario}
            className={`px-8 py-4 bg-transparent border-2 font-mono font-bold tracking-widest uppercase transition-all duration-300 disabled:opacity-30 disabled:cursor-not-allowed ${
              simulationMode
                ? 'border-purple-400 text-purple-400 hover:bg-purple-400 hover:text-[#0f0f11] shadow-[0_0_15px_rgba(168,85,247,0.5)]'
                : 'border-[#00ffcc] text-[#00ffcc] hover:bg-[#00ffcc] hover:text-[#0f0f11] shadow-[0_0_15px_rgba(0,255,204,0.5)]'
            }`}
          >
            {simulationMode ? 'Start Simulation' : 'Connect to Bunker'}
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
            onClick={handleDisconnect}
            className="px-8 py-4 bg-transparent border-2 border-[#ff3333] text-[#ff3333] font-mono font-bold tracking-widest uppercase hover:bg-[#ff3333] hover:text-[#0f0f11] transition-all duration-300 shadow-[0_0_15px_rgba(255,51,51,0.5)] flex items-center gap-3"
          >
            <Mic className="w-5 h-5" />
            Disconnect
          </button>
        )}
      </div>

      {/* Terminal UI — collapsible, bottom-right */}
      <div className={`absolute bottom-6 right-6 z-30 transition-all duration-300 ${logsExpanded ? 'w-96' : 'w-48'}`}>
        <button
          onClick={() => setLogsExpanded(!logsExpanded)}
          className="w-full flex items-center justify-between bg-black/60 border border-gray-800 px-3 py-2 rounded backdrop-blur-sm text-gray-500 hover:text-[#00ffcc] transition-colors"
        >
          <div className="flex items-center gap-2">
            <Terminal className="w-4 h-4" />
            <span className="text-xs font-mono uppercase tracking-wider">System Logs</span>
          </div>
          {logsExpanded ? <ChevronDown className="w-4 h-4" /> : <ChevronUp className="w-4 h-4" />}
        </button>
        {logsExpanded && (
          <div className="mt-1 bg-black/60 border border-gray-800 p-3 rounded backdrop-blur-sm">
            <div className="h-40 overflow-y-auto font-mono text-sm text-[#00ffcc] flex flex-col gap-1">
              {logs.map((log, i) => (
                <div key={i} className="opacity-80 hover:opacity-100">{log}</div>
              ))}
              {logs.length === 0 && <div className="text-gray-600 italic">Awaiting connection...</div>}
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

export default App;
