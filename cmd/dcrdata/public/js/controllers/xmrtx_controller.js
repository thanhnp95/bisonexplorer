import { Controller } from '@hotwired/stimulus'
import { requestJSON } from '../helpers/http'

async function requestWithTimeout (url, ms = 90_000) {
  const ctrl = new AbortController()
  const timer = setTimeout(() => ctrl.abort(), ms)
  try {
    return await requestJSON(url, { signal: ctrl.signal })
  } finally {
    clearTimeout(timer)
  }
}

export default class extends Controller {
  static get targets () {
    return ['inputAddress', 'inputViewKey', 'inputTxPrivateKey',
      'inputReceiptAddress', 'decodeOuputsMsg', 'proveTxMsg', 'decodePanel',
      'decodeResult', 'decodeAddressRes', 'viewkeyRes', 'totalDecodeAmount',
      'provePanel', 'proveResult', 'proveAddressRes', 'txKeyRes', 'totalProveAmount',
      'decodeOutputBody']
  }

  async connect () {
    this.txid = this.data.get('txid')
    this.actionType = 'decode' // second is 'prove'
    // collect links and panes inside this controller element
    this.links = Array.from(this.element.querySelectorAll('.tx-tools .nav a'))
    this.panes = Array.from(this.element.querySelectorAll('.tx-tools .tab-pane'))

    // wire clicks
    this.links.forEach(a => a.addEventListener('click', ev => {
      ev.preventDefault()
      const href = a.getAttribute('href')
      this.showTab(href)
      if (window.history && window.history.replaceState) {
        window.history.replaceState(null, '', href)
      }
    }))

    // initial: try hash, else first link (or existing .active)
    const hash = window.location.hash
    if (hash && this.element.querySelector(hash)) {
      this.showTab(hash)
    } else {
      const activeLink = this.element.querySelector('.tx-tools .nav li.active a') || this.links[0]
      if (activeLink) this.showTab(activeLink.getAttribute('href'))
    }
  }

  showTab (idOrHash) {
    if (!idOrHash) return
    const id = idOrHash.startsWith('#') ? idOrHash : `#${idOrHash}`

    this.links.forEach(a => {
      const li = a.closest('li')
      if (a.getAttribute('href') === id) {
        if (li) li.classList.add('active')
        a.setAttribute('aria-selected', 'true')
      } else {
        if (li) li.classList.remove('active')
        a.setAttribute('aria-selected', 'false')
      }
    })

    this.panes.forEach(p => {
      if (`#${p.id}` === id) {
        p.classList.add('active')
        p.removeAttribute('hidden')
      } else {
        p.classList.remove('active')
        p.setAttribute('hidden', '')
      }
    })
  }

  async handlerDecodeOutputs () {
    this.decodeOuputsMsgTarget.classList.add('d-none')
    if (!this.txid || this.txid === '') {
      this.decodeOuputsMsgTarget.classList.remove('d-none')
      this.decodeOuputsMsgTarget.textContent = 'an error occurred.'
      return
    }
    // get address
    const address = this.inputAddressTarget.value.trim()
    const viewkey = this.inputViewKeyTarget.value.trim()
    if (address === '' || viewkey === '') {
      this.decodeOuputsMsgTarget.classList.remove('d-none')
      this.decodeOuputsMsgTarget.textContent = 'data not entered correctly'
      return
    }
    this.decodePanelTarget.classList.add('tag-loading')
    // call api
    const url = `/api/xmr/decode-output?txid=${this.txid}&address=${address}&viewkey=${viewkey}`
    let response
    try {
      response = await requestWithTimeout(url, 120_000)
    } catch (err) {
      this.decodePanelTarget.classList.remove('tag-loading')
      if (err.name === 'AbortError') {
        this.decodeOuputsMsgTarget.classList.remove('d-none')
        this.decodeOuputsMsgTarget.textContent = 'The request timed out or failed. Please check or try again.'
        return
      } else {
        this.decodeOuputsMsgTarget.classList.remove('d-none')
        this.decodeOuputsMsgTarget.textContent = 'an error occurred.'
        return
      }
    }
    this.decodePanelTarget.classList.remove('tag-loading')
    if (!response) {
      this.decodeOuputsMsgTarget.classList.remove('d-none')
      this.decodeOuputsMsgTarget.textContent = 'get response from api failed'
      return
    }
    if (response.err) {
      this.decodeOuputsMsgTarget.classList.remove('d-none')
      this.decodeOuputsMsgTarget.textContent = response.msg
      return
    }
    const decodeData = response.decodeData
    this.decodePanelTarget.classList.add('d-none')
    this.decodeResultTarget.classList.remove('d-none')
    this.decodeAddressResTarget.textContent = address
    this.viewkeyResTarget.textContent = this.maskMiddle(viewkey)

    // hide main input/outputs table
    $('#mainInOut').addClass('d-none')
    // show special outputs table
    $('#decodeOutputs').removeClass('d-none')

    // calculate amount for sum outputs amount
    let sumAmount = 0
    let outputHtml = ''
    decodeData.forEach((output, idx) => {
      if (output.match) {
        sumAmount += output.amount
      }
      outputHtml += `<tr>
                        <td class="shrink-to-fit ${output.match ? 'fw-bold' : ''}">${idx}</td>
                        <td class="position-relative clipboard ${output.match ? 'fw-bold' : ''}">
                           ${output.output_pubkey}
                        </td>
                        <td class="fs13 break-word shrink-to-fit ${output.match ? 'fw-bold active-color' : ''}">
                          ${output.amount > 0 ? (output.amount / 1e12).toFixed(8) : 'N/A'} 
                        </td>
                        <td class="fs13 break-word shrink-to-fit ${output.match ? 'fw-bold active-color' : ''}">
                           ${output.match}
                        </td>
                     </tr>`
    })
    this.decodeOutputBodyTarget.innerHTML = outputHtml
    this.totalDecodeAmountTarget.textContent = (sumAmount / 1e12).toFixed(8)
  }

  async handlerProveTx () {
    this.proveTxMsgTarget.classList.add('d-none')
    if (!this.txid || this.txid === '') {
      this.proveTxMsgTarget.classList.remove('d-none')
      this.proveTxMsgTarget.textContent = 'an error occurred.'
      return
    }
    // get address
    const address = this.inputReceiptAddressTarget.value.trim()
    const txkey = this.inputTxPrivateKeyTarget.value.trim()
    if (address === '' || txkey === '') {
      this.proveTxMsgTarget.classList.remove('d-none')
      this.proveTxMsgTarget.textContent = 'data not entered correctly'
      return
    }
    this.provePanelTarget.classList.add('tag-loading')
    // call api
    const url = `/api/xmr/prove-tx?txid=${this.txid}&address=${address}&txkey=${txkey}`
    let response
    try {
      response = await requestWithTimeout(url, 120_000)
    } catch (err) {
      this.provePanelTarget.classList.remove('tag-loading')
      if (err.name === 'AbortError') {
        this.proveTxMsgTarget.classList.remove('d-none')
        this.proveTxMsgTarget.textContent = 'The request timed out or failed. Please check or try again.'
        return
      } else {
        this.proveTxMsgTarget.classList.remove('d-none')
        this.proveTxMsgTarget.textContent = 'an error occurred.'
        return
      }
    }
    this.provePanelTarget.classList.remove('tag-loading')
    if (!response) {
      this.proveTxMsgTarget.classList.remove('d-none')
      this.proveTxMsgTarget.textContent = 'get response from api failed'
      return
    }
    if (response.err) {
      this.proveTxMsgTarget.classList.remove('d-none')
      this.proveTxMsgTarget.textContent = response.msg
      return
    }
    const proveData = response.proveData
    this.provePanelTarget.classList.add('d-none')
    this.proveResultTarget.classList.remove('d-none')
    this.proveAddressResTarget.textContent = address
    this.txKeyResTarget.textContent = this.maskMiddle(txkey)

    // hide main input/outputs table
    $('#mainInOut').addClass('d-none')
    // show special outputs table
    $('#decodeOutputs').removeClass('d-none')

    // calculate amount for sum outputs amount
    let sumAmount = 0
    let outputHtml = ''
    proveData.forEach((output, idx) => {
      if (output.match) {
        sumAmount += output.amount
      }
      outputHtml += `<tr>
                        <td class="shrink-to-fit ${output.match ? 'fw-bold' : ''}">${idx}</td>
                        <td class="position-relative clipboard ${output.match ? 'fw-bold' : ''}">
                           ${output.output_pubkey}
                        </td>
                        <td class="fs13 break-word shrink-to-fit ${output.match ? 'fw-bold active-color' : ''}">
                          ${output.amount > 0 ? (output.amount / 1e12).toFixed(8) : 'N/A'} 
                        </td>
                        <td class="fs13 break-word shrink-to-fit ${output.match ? 'fw-bold active-color' : ''}">
                           ${output.match}
                        </td>
                     </tr>`
    })
    this.decodeOutputBodyTarget.innerHTML = outputHtml
    this.totalProveAmountTarget.textContent = (sumAmount / 1e12).toFixed(8)
  }

  backToDecodeInputs () {
    this.decodeResultTarget.classList.add('d-none')
    this.decodePanelTarget.classList.remove('d-none')
    this.inputAddressTarget.value = ''
    this.inputViewKeyTarget.value = ''
    // hide result output of decode
    $('#decodeOutputs').addClass('d-none')
    // show main in/out table
    $('#mainInOut').removeClass('d-none')
  }

  backToProveInputs () {
    this.proveResultTarget.classList.add('d-none')
    this.provePanelTarget.classList.remove('d-none')
    this.inputTxPrivateKeyTarget.value = ''
    this.inputReceiptAddressTarget.value = ''
    // hide result output of decode
    $('#decodeOutputs').addClass('d-none')
    // show main in/out table
    $('#mainInOut').removeClass('d-none')
  }

  maskMiddle (s, visible = 3, maskChar = '*') {
    if (s == null) return ''
    s = String(s)
    if (visible < 0) visible = 0
    const len = s.length
    if (len <= 2 * visible) return s // too short -> return unchanged
    const start = s.slice(0, visible)
    const end = s.slice(len - visible)
    const middleLen = len - 2 * visible
    return start + maskChar.repeat(middleLen) + end
  }
}
