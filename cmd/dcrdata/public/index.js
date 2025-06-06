// import 'core-js/stable';
import 'regenerator-runtime/runtime'
/* global require */
import ws from './js/services/messagesocket_service'
import { Application } from '@hotwired/stimulus'
import { definitionsFromContext } from '@hotwired/stimulus-webpack-helpers'
import { darkEnabled } from './js/services/theme_service'
import globalEventBus from './js/services/event_bus_service'

require('./scss/application.scss')

window.darkEnabled = darkEnabled

const application = Application.start()
const context = require.context('./js/controllers', true, /\.js$/)
application.load(definitionsFromContext(context))

document.addEventListener('turbolinks:load', function (e) {
  document.querySelectorAll('.jsonly').forEach((el) => {
    el.classList.remove('jsonly')
  })
})

export function notifyNewBlock (newBlock) {
  if (window.Notification.permission !== 'granted') return
  const block = newBlock.block
  const newBlockNtfn = new window.Notification('New Decred Block Mined', {
    body: `Block mined at height <b>${block.height}</b>`,
    icon: '/images/dcrdata144x128.png',
    notifyError: (e) => console.error('Error showing notification:', e)
  })
  setTimeout(() => newBlockNtfn.close(), 3000)
}

function getSocketURI (loc) {
  const protocol = (loc.protocol === 'https:') ? 'wss' : 'ws'
  return protocol + '://' + loc.host + '/ws'
}

function sleep (ms) {
  return new Promise(resolve => setTimeout(resolve, ms))
}

async function createWebSocket (loc) {
  // wait a bit to prevent websocket churn from drive by page loads
  const uri = getSocketURI(loc)
  await sleep(300)
  ws.connect(uri)

  const updateBlockData = function (event) {
    const newBlock = JSON.parse(event)
    if (window.loggingDebug) {
      console.log('Block received:', newBlock)
    }
    newBlock.block.unixStamp = new Date(newBlock.block.time).getTime() / 1000
    globalEventBus.publish('BLOCK_RECEIVED', newBlock)
  }

  const updateSummaryData = function (event) {
    const summary = JSON.parse(event)
    if (window.loggingDebug) {
      console.log('Decred summary received:', summary)
    }
    globalEventBus.publish('SUMMARY_RECEIVED', summary)
  }

  const update24hData = function (event) {
    const summary24h = JSON.parse(event)
    if (window.loggingDebug) {
      console.log('Decred summary 24h received:', summary24h)
    }
    globalEventBus.publish('SUMMARY_24H_RECEIVED', summary24h)
  }

  const updateLtcBlockData = function (event) {
    const newBlock = JSON.parse(event)
    if (window.loggingDebug) {
      console.log('LTC Block received:', newBlock)
    }
    newBlock.block.unixStamp = new Date(newBlock.block.time).getTime() / 1000
    globalEventBus.publish('LTC_BLOCK_RECEIVED', newBlock)
  }

  const updateBtcBlockData = function (event) {
    const newBlock = JSON.parse(event)
    if (window.loggingDebug) {
      console.log('BTC Block received:', newBlock)
    }
    newBlock.block.unixStamp = new Date(newBlock.block.time).getTime() / 1000
    globalEventBus.publish('BTC_BLOCK_RECEIVED', newBlock)
  }

  const updateLtcMempoolData = function (event) {
    const newMempool = JSON.parse(event)
    if (window.loggingDebug) {
      console.log('Mempool LTC received:', newMempool)
    }
    globalEventBus.publish('MEMPOOL_LTC_RECEIVED', newMempool)
  }

  const updateBtcMempoolData = function (event) {
    const newMempool = JSON.parse(event)
    if (window.loggingDebug) {
      console.log('Mempool BTC received:', newMempool)
    }
    globalEventBus.publish('MEMPOOL_BTC_RECEIVED', newMempool)
  }

  ws.registerEvtHandler('newblock', updateBlockData)
  ws.registerEvtHandler('newltcblock', updateLtcBlockData)
  ws.registerEvtHandler('newbtcblock', updateBtcBlockData)
  ws.registerEvtHandler('newltcmempool', updateLtcMempoolData)
  ws.registerEvtHandler('newbtcmempool', updateBtcMempoolData)
  ws.registerEvtHandler('summaryinfo', updateSummaryData)
  ws.registerEvtHandler('summary24h', update24hData)
  ws.registerEvtHandler('exchange', e => {
    globalEventBus.publish('EXCHANGE_UPDATE', JSON.parse(e))
  })
}

// Debug logging can be enabled by entering logDebug(true) in the console.
// Your setting will persist across sessions.
window.loggingDebug = window.localStorage.getItem('loggingDebug') === '1'
window.logDebug = yes => {
  window.loggingDebug = yes
  window.localStorage.setItem('loggingDebug', yes ? '1' : '0')
  return 'debug logging set to ' + (yes ? 'true' : 'false')
}

createWebSocket(window.location)
globalEventBus.on('BLOCK_RECEIVED', notifyNewBlock)
