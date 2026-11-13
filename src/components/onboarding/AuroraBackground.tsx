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
        backgroundColor: '#0D1421',
      }}
    >
      {/* Revolut-style deep blue glow — left side */}
      <div
        className="aurora-blob animate-aurora1"
        style={{
          position: 'absolute',
          width: '60vw',
          height: '80vh',
          top: '-10%',
          left: '-20%',
          background: 'radial-gradient(ellipse at center, rgba(20,60,120,0.6) 0%, rgba(15,40,80,0.3) 40%, transparent 70%)',
          filter: 'blur(80px)',
        }}
      />
      {/* Secondary teal accent — bottom left */}
      <div
        className="aurora-blob animate-aurora2"
        style={{
          position: 'absolute',
          width: '50vw',
          height: '50vh',
          bottom: '-15%',
          left: '-10%',
          background: 'radial-gradient(ellipse at center, rgba(15,80,120,0.4) 0%, transparent 70%)',
          filter: 'blur(100px)',
        }}
      />
      {/* Subtle top-right warmth */}
      <div
        className="aurora-blob animate-aurora3"
        style={{
          position: 'absolute',
          width: '30vw',
          height: '30vh',
          top: '10%',
          right: '5%',
          background: 'radial-gradient(ellipse at center, rgba(20,45,80,0.25) 0%, transparent 70%)',
          filter: 'blur(60px)',
        }}
      />
    </div>
  );
}
