class SpeechCaptureProcessor extends AudioWorkletProcessor {
  constructor(options) {
    super();
    this.limit = options.processorOptions.maxSamples;
    this.count = 0;
    this.phase = 0;
    this.sum = 0;
    this.weight = 0;
    this.frame = new Float32Array(320);
    this.used = 0;
    this.active = true;
    this.pendingFrames = 0;
    this.port.onmessage = ({ data }) => {
      if (data === "stop") this.finish();
      if (data === "ack") this.pendingFrames--;
    };
  }
  emit() {
    if (!this.used) return;
    if (this.pendingFrames >= 50) { this.active = false; this.port.postMessage({ type: "error" }); return; }
    this.pendingFrames++;
    const frame = this.frame.slice(0, this.used);
    this.port.postMessage({ type: "frame", frame }, [frame.buffer]);
    this.used = 0;
  }
  finish() {
    if (!this.active) return;
    this.active = false;
    this.emit();
    this.port.postMessage({ type: "stopped" });
  }
  process(inputs) {
    if (!this.active) return true;
    const channels = inputs[0];
    if (!channels?.length) return true;
    const ratio = sampleRate / 16000;
    for (let i = 0; i < channels[0].length; i++) {
      let sample = 0;
      for (const channel of channels) sample += channel[i] / channels.length;
      let remaining = 1;
      while (remaining > 0) {
        const amount = Math.min(remaining, ratio - this.phase);
        this.sum += sample * amount;
        this.weight += amount;
        this.phase += amount;
        remaining -= amount;
        if (this.phase + 1e-6 >= ratio) {
          this.frame[this.used++] = this.sum / this.weight;
          this.count++;
          this.phase = this.sum = this.weight = 0;
          if (this.used === 320) { this.emit(); if (!this.active) return true; }
          if (this.count >= this.limit) { this.finish(); return true; }
        }
      }
    }
    return true;
  }
}
registerProcessor("speech-capture", SpeechCaptureProcessor);
