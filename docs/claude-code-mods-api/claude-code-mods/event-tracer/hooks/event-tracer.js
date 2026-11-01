// Writes one line to the debug log, so a side pane can show events as they fire
function trace(mods, event_name, e) {
  const detail = e.tool ? ' ' + e.tool : '' // tool name 
  mods.ui.log('EVENT ' + event_name + detail, { to: 'debug' })
}

// Claude Code calls this once when the mod loads.
// Every hook logs its event, then lets it carry on unchanged.
export function register(on) {
  on('session.start', async (mods, e, next) => { 
    trace(mods, 'session.start', e); return next(e) 
  })

  on('prompt.submit', async (mods, e, next) => { 
    trace(mods, 'prompt.submit', e); return next(e) 
  })
  
  on('turn.start', async (mods, e, next) => { 
    trace(mods, 'turn.start', e); return next(e) 
  })

  on('tool.call', async (mods, e, next) => { 
    trace(mods, 'tool.call', e); return next(e) 
  })

  on('tool.check', async (mods, e, next) => { 
    trace(mods, 'tool.check', e); return next(e) 
  })

  on('agent.spawn', async (mods, e, next) => { 
    trace(mods, 'agent.spawn', e); return next(e) 
  })

  on('turn.complete', async (mods, e, next) => { 
    trace(mods, 'turn.complete', e); return next(e) 
  })

  on('command.run', async (mods, e, next) => { 
    trace(mods, 'command.run', e); return next(e) 
  })

  on('session.compact', async (mods, e, next) => { 
    trace(mods, 'session.compact', e); return next(e) 
  })

  on('session.end', async (mods, e, next) => { 
    trace(mods, 'session.end', e); return next(e) 
  })
}
