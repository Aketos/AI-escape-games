declare module 'opus-recorder' {
  export interface RecorderConfig {
    encoderPath?: string;
    encoderSampleRate?: number;
    encoderFrameSize?: number;
    encoderApplication?: number;
    encoderComplexity?: number;
    numberOfChannels?: number;
    streamPages?: boolean;
    maxFramesPerPage?: number;
    bufferLength?: number;
    recordingGain?: number;
    resampleQuality?: number;
    sourceNode?: AudioNode;
    mediaTrackConstraints?: MediaTrackConstraints | boolean;
  }

  export default class Recorder {
    constructor(config?: RecorderConfig);
    ondataavailable?: (data: Uint8Array) => void;
    onstart?: () => void;
    onstop?: () => void;
    start(): Promise<void>;
    stop(): Promise<void>;
    pause(): void;
    resume(): void;
    setRecordingGain(gain: number): void;
  }
}

declare module 'opus-recorder/dist/encoderWorker.min.js?url' {
  const url: string;
  export default url;
}
