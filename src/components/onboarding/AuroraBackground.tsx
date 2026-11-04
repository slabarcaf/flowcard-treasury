export function AuroraBackground() {
  return (
    <div
      className="aurora-container"
      style={{
        position: 'fixed',
        inset: 0,
        zIndex: 0,
        overflow: 'hidden',
        pointerEvents: 'none',
      }}
    >
      {/* Blob 1 — top-left purple */}
      <div
        className="aurora-blob animate-aurora1"
        style={{
          position: 'absolute',
          width: '45vw',
          height: '45vw',
          top: '-10%',
          left: '-10%',
          background: 'radial-gradient(circle, rgba(82,102,235,0.35) 0%, transparent 70%)',
          filter: 'blur(100px)',
        }}
      />
      {/* Blob 2 — top-right lavender */}
      <div
        className="aurora-blob animate-aurora2"
        style={{
          position: 'absolute',
          width: '40vw',
          height: '40vw',
          top: '-5%',
          right: '-8%',
          background: 'radial-gradient(circle, rgba(196,181,253,0.4) 0%, transparent 70%)',
          filter: 'blur(90px)',
        }}
      />
      {/* Blob 3 — bottom-left ice-blue */}
      <div
        className="aurora-blob animate-aurora3"
        style={{
          position: 'absolute',
          width: '50vw',
          height: '50vw',
          bottom: '-15%',
          left: '-5%',
          background: 'radial-gradient(circle, rgba(147,197,253,0.3) 0%, transparent 70%)',
          filter: 'blur(120px)',
        }}
      />
      {/* Blob 4 — bottom-right soft pink */}
      <div
        className="aurora-blob animate-aurora4"
        style={{
          position: 'absolute',
          width: '35vw',
          height: '35vw',
          bottom: '-8%',
          right: '-5%',
          background: 'radial-gradient(circle, rgba(221,214,254,0.35) 0%, transparent 70%)',
          filter: 'blur(80px)',
        }}
      />
    </div>
  );
}
