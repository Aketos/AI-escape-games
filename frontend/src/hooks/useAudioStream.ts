import { useState, useEffect, useRef, useCallback } from 'react';
import Recorder from 'opus-recorder';
import encoderPath from 'opus-recorder/dist/encoderWorker.min.js?url';
import { OggOpusDecoderWebWorker } from 'ogg-opus-decoder';

type ConnectionStatus = 'Idle' | 'Booting' | 'Ready' | 'Connected' | 'Disconnected';

interface UseAudioStreamReturn {
  status: ConnectionStatus;
  connect: () => void;
  disconnect: () => void;
  micVolume: number;
  aiVolume: number;
  logs: string[];
  onGameState: (cb: (data: unknown) => void) => void;
  onIntroComplete: (cb: () => void) => void;
}

export function useAudioStream(url: string): UseAudioStreamReturn {
  const [status, setStatus] = useState<ConnectionStatus>('Idle');
  const [logs, setLogs] = useState<string[]>([]);
  const [micVolume, setMicVolume] = useState(0);
  const [aiVolume, setAiVolume] = useState(0);

  const [isAISpeaking, setIsAISpeaking] = useState(false);

  const gameStateCallbackRef = useRef<((data: unknown) => void) | null>(null);
  const introCompleteCallbackRef = useRef<(() => void) | null>(null);

  const wsRef = useRef<WebSocket | null>(null);
  const audioCtxRef = useRef<AudioContext | null>(null);
  const mediaStreamRef = useRef<MediaStream | null>(null);
  const ambientAudioRef = useRef<HTMLAudioElement | null>(null);
  const aiSilenceTimerRef = useRef<number | null>(null);
  const recorderRef = useRef<Recorder | null>(null);
  const decoderRef = useRef<OggOpusDecoderWebWorker | null>(null);
  const micAnalyserTimerRef = useRef<number | null>(null);
  const nextPlayTimeRef = useRef(0);

  const addLog = useCallback((msg: string) => {
    setLogs((prev) => [...prev, `[${new Date().toLocaleTimeString()}] ${msg}`].slice(-10));
  }, []);

  // Ducking logic for ambient drone
  useEffect(() => {
    if (!ambientAudioRef.current) return;
    const targetVolume = isAISpeaking ? 0.1 : 0.4;
    const duration = isAISpeaking ? 200 : 1000; // ms
    const steps = 20;
    const stepTime = duration / steps;
    const volumeStep = (targetVolume - ambientAudioRef.current.volume) / steps;

    let currentStep = 0;
    const interval = setInterval(() => {
      if (!ambientAudioRef.current) {
        clearInterval(interval);
        return;
      }
      currentStep++;
      let nextVol = ambientAudioRef.current.volume + volumeStep;
      nextVol = Math.max(0, Math.min(1, nextVol));
      ambientAudioRef.current.volume = nextVol;

      if (currentStep >= steps) {
        ambientAudioRef.current.volume = targetVolume;
        clearInterval(interval);
      }
    }, stepTime);

    return () => clearInterval(interval);
  }, [isAISpeaking]);

  const connect = async () => {
    if (wsRef.current) return;
    
    addLog("Requesting Microphone...");
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: { echoCancellation: true, noiseSuppression: true, autoGainControl: true } });
      mediaStreamRef.current = stream;
      
      const audioCtx = new (window.AudioContext || (window as any).webkitAudioContext)();
      audioCtxRef.current = audioCtx;
      nextPlayTimeRef.current = 0;

      // WASM Opus decoder for Moshi's Ogg Opus output stream
      const decoder = new OggOpusDecoderWebWorker();
      decoderRef.current = decoder;
      await decoder.ready;
      
      addLog("Microphone ready. Connecting to WebSocket...");
      const ws = new WebSocket(url);
      wsRef.current = ws;

      if (!ambientAudioRef.current) {
        const bgm = new Audio('/sounds/ambient_drone.opus');
        bgm.loop = true;
        bgm.volume = 0.4;
        bgm.play().catch(e => console.warn("Ambient drone play failed", e));
        ambientAudioRef.current = bgm;
      }

      ws.onopen = () => {
        setStatus('Ready');
        addLog("WebSocket Connected.");
        startStreaming(stream, audioCtx, ws);
      };

      ws.onmessage = (event) => {
        try {
          const data = JSON.parse(event.data);
          if (data.type === 'status') {
            setStatus(data.payload.includes('Booting') ? 'Booting' : 'Ready');
            addLog(data.payload);
          } else if (data.type === 'sfx_trigger') {
            addLog(`SFX Trigger: ${data.payload}`);
            const audio = new Audio(`/sounds/${data.payload}.opus`);
            audio.volume = 0.85; // Set default SFX volume high enough to be immersive
            audio.play().catch(e => console.warn(`Could not play SFX ${data.payload}.opus:`, e));
          } else if (data.type === 'ai_audio_chunk') {
            setIsAISpeaking(true);
            if (aiSilenceTimerRef.current) {
              clearTimeout(aiSilenceTimerRef.current);
            }
            aiSilenceTimerRef.current = window.setTimeout(() => {
              setIsAISpeaking(false);
            }, 500);

            playAudioChunk(data.payload, audioCtx);
          } else if (data.type === 'game_state') {
            if (gameStateCallbackRef.current) {
              gameStateCallbackRef.current(data.payload);
            }
          } else if (data.type === 'intro_complete') {
            if (introCompleteCallbackRef.current) {
              introCompleteCallbackRef.current();
            }
          }
        } catch (e) {
          console.error("Failed to parse message", e);
        }
      };

      ws.onclose = () => {
        setStatus('Disconnected');
        addLog("WebSocket Closed.");
        cleanup();
      };
      
    } catch (err) {
      addLog(`Error: ${err}`);
      console.error(err);
    }
  };

  const startStreaming = (stream: MediaStream, audioCtx: AudioContext, ws: WebSocket) => {
    setStatus('Connected');
    const source = audioCtx.createMediaStreamSource(stream);

    // Mic volume meter (AnalyserNode, no audio processing in JS)
    const analyser = audioCtx.createAnalyser();
    analyser.fftSize = 1024;
    source.connect(analyser);
    const timeData = new Float32Array(analyser.fftSize);
    micAnalyserTimerRef.current = window.setInterval(() => {
      analyser.getFloatTimeDomainData(timeData);
      let sum = 0;
      for (let i = 0; i < timeData.length; i++) {
        sum += timeData[i] * timeData[i];
      }
      setMicVolume(Math.sqrt(sum / timeData.length));
    }, 100);

    // Moshi requires a CONTINUOUS Ogg Opus stream at 24kHz (silence included).
    // opus-recorder encodes the mic and emits Ogg pages via ondataavailable.
    const recorder = new Recorder({
      encoderPath,
      encoderSampleRate: 24000,
      encoderFrameSize: 20,
      encoderApplication: 2049,
      numberOfChannels: 1,
      streamPages: true,
      maxFramesPerPage: 2,
      recordingGain: 1,
      resampleQuality: 3,
      sourceNode: source,
    });
    recorderRef.current = recorder;

    recorder.ondataavailable = (page: Uint8Array) => {
      if (ws.readyState !== WebSocket.OPEN || page.length === 0) return;
      let binary = '';
      for (let i = 0; i < page.byteLength; i++) {
        binary += String.fromCharCode(page[i]);
      }
      ws.send(JSON.stringify({ type: 'audio_chunk', payload: btoa(binary) }));
    };

    recorder.start().catch((e) => {
      addLog(`Recorder error: ${e}`);
      console.error('Opus recorder failed to start', e);
    });
  };

  const playAudioChunk = (base64: string, audioCtx: AudioContext) => {
    const binary = atob(base64);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
      bytes[i] = binary.charCodeAt(i);
    }

    const decoder = decoderRef.current;
    if (!decoder) return;

    // Moshi streams Ogg Opus pages; decode to Float32 PCM then schedule playback.
    decoder.decode(bytes).then(({ channelData, samplesDecoded, sampleRate }) => {
      if (samplesDecoded <= 0) return;
      const floatData = channelData[0];

      let sum = 0;
      for (let i = 0; i < samplesDecoded; i++) {
        sum += floatData[i] * floatData[i];
      }
      setAiVolume(Math.sqrt(sum / samplesDecoded));

      const audioBuffer = audioCtx.createBuffer(1, samplesDecoded, sampleRate);
      audioBuffer.getChannelData(0).set(floatData.subarray(0, samplesDecoded));

      const src = audioCtx.createBufferSource();
      src.buffer = audioBuffer;
      src.connect(audioCtx.destination);

      const currentTime = audioCtx.currentTime;
      if (nextPlayTimeRef.current < currentTime) {
        nextPlayTimeRef.current = currentTime;
      }
      src.start(nextPlayTimeRef.current);
      nextPlayTimeRef.current += audioBuffer.duration;
    }).catch((e: unknown) => {
      console.error('Opus decode failed', e);
    });
  };

  const disconnect = () => {
    cleanup();
  };

  const cleanup = () => {
    if (recorderRef.current) {
      recorderRef.current.stop().catch(() => {});
      recorderRef.current = null;
    }
    if (decoderRef.current) {
      decoderRef.current.free();
      decoderRef.current = null;
    }
    if (micAnalyserTimerRef.current) {
      clearInterval(micAnalyserTimerRef.current);
      micAnalyserTimerRef.current = null;
    }
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    if (mediaStreamRef.current) {
      mediaStreamRef.current.getTracks().forEach(t => t.stop());
      mediaStreamRef.current = null;
    }
    if (audioCtxRef.current) {
      audioCtxRef.current.close();
      audioCtxRef.current = null;
    }
    if (ambientAudioRef.current) {
      ambientAudioRef.current.pause();
      ambientAudioRef.current = null;
    }
    if (aiSilenceTimerRef.current) {
      clearTimeout(aiSilenceTimerRef.current);
      aiSilenceTimerRef.current = null;
    }
    setIsAISpeaking(false);
    setStatus('Idle');
    setMicVolume(0);
    setAiVolume(0);
  };

  useEffect(() => {
    return () => cleanup();
  }, []);

  const onGameState = useCallback((cb: (data: unknown) => void) => {
    gameStateCallbackRef.current = cb;
  }, []);

  const onIntroComplete = useCallback((cb: () => void) => {
    introCompleteCallbackRef.current = cb;
  }, []);

  return { status, connect, disconnect, micVolume, aiVolume, logs, onGameState, onIntroComplete };
}
