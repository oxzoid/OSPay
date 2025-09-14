import React, { useState, useEffect } from 'react'
import { v4 as uuidv4 } from 'uuid'
import QRCode from 'react-qr-code'

const API_BASE = import.meta.env.VITE_API_BASE || 'http://localhost:8080'

interface Order {
  id: string
  merchant_id: string
  amount_minor: number
  asset: string
  chain: string
  status: string
  deposit_address: string
  tx_hash?: string
  paid_at?: string
}

interface OrderCreateResponse {
  order_id: string
  deposit_address: string
  status: string
}

interface Merchant {
  id: string
  api_key: string
  merchant_wallet_address: string
}

export default function RealTest() {
  const [step, setStep] = useState<'setup' | 'payment' | 'checking' | 'complete'>('setup')
  const [merchant, setMerchant] = useState<Merchant | null>(null)
  const [order, setOrder] = useState<Order | null>(null)
  const [orderId, setOrderId] = useState<string>('')
  const [txHash, setTxHash] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [pollingInterval, setPollingInterval] = useState<number | null>(null)

  // Form inputs
  const [merchantWallet, setMerchantWallet] = useState('')
  const [amount, setAmount] = useState(1.0)
  const [chain, setChain] = useState('BSC')
  const [tokenContract, setTokenContract] = useState('0x55d398326f99059ff775485246999027b3197955') // Default BSC-USD

  const createMerchantAndOrder = async () => {
    if (!merchantWallet.trim()) {
      setError('Please enter your wallet address')
      return
    }

    setLoading(true)
    setError('')
    
    try {
      // Create merchant with your wallet
      const merchantRes = await fetch(`${API_BASE}/merchants`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          name: `Real Test Merchant ${Date.now()}`,
          merchant_wallet_address: merchantWallet.trim()
        })
      })
      
      if (!merchantRes.ok) {
        const errorText = await merchantRes.text()
        throw new Error(`Failed to create merchant: ${merchantRes.status} - ${errorText}`)
      }
      
      const merchantData = await merchantRes.json()
      setMerchant(merchantData)

      // Create order
      const orderRes = await fetch(`${API_BASE}/orders`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-API-Key': merchantData.api_key
        },
        body: JSON.stringify({
          merchant_id: merchantData.id,
          amount_minor: Math.round(amount * 1000000000000000000), // Convert to minor units (18 decimals wei-style)
          asset: 'USDT',
          chain: chain,
          idempotency_key: uuidv4()
        })
      })
      
      if (!orderRes.ok) {
        const errorText = await orderRes.text()
        throw new Error(`Failed to create order: ${orderRes.status} - ${errorText}`)
      }
      
      const orderData: OrderCreateResponse = await orderRes.json()
      setOrderId(orderData.order_id)
      
      // Create temp order object for display
      const tempOrder: Order = {
        id: orderData.order_id,
        merchant_id: merchantData.id,
        amount_minor: Math.round(amount * 1000000000000000000),
        asset: 'USDT',
        chain: chain,
        status: orderData.status,
        deposit_address: orderData.deposit_address
      }
      setOrder(tempOrder)
      setStep('payment')
      
    } catch (err) {
      setError(`Setup failed: ${err}`)
    }
    setLoading(false)
  }

  const submitTransaction = async () => {
    if (!txHash.trim() || !merchant || !orderId) return
    
    setLoading(true)
    setError('')
    
    try {
      const res = await fetch(`${API_BASE}/events/payment-detected`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'X-API-Key': merchant.api_key
        },
        body: JSON.stringify({
          order_id: orderId,
          tx_hash: txHash.trim()
        })
      })
      
      if (!res.ok) {
        const errorText = await res.text()
        throw new Error(`Verification failed: ${res.status} - ${errorText}`)
      }
      
      setStep('checking')
      startPolling()
      
    } catch (err) {
      setError(`Failed to submit transaction: ${err}`)
    }
    setLoading(false)
  }

  const startPolling = () => {
    const interval = setInterval(async () => {
      if (!orderId || !merchant) return
      
      try {
        const res = await fetch(`${API_BASE}/orders/get?id=${orderId}`, {
          headers: { 'X-API-Key': merchant.api_key }
        })
        
        if (res.ok) {
          const data = await res.json()
          setOrder(data)
          
          if (data.status === 'PAID') {
            setStep('complete')
            if (pollingInterval) clearInterval(pollingInterval)
            return
          }
          
          // If status is not PENDING or CONFIRMING, something went wrong
          if (data.status !== 'PENDING' && data.status !== 'CONFIRMING') {
            setError(`Payment verification failed. Status: ${data.status}`)
            if (pollingInterval) clearInterval(pollingInterval)
            setStep('payment')
          }
        }
      } catch (err) {
        console.error('Polling error:', err)
      }
    }, 3000)
    
    setPollingInterval(interval)
    
    // Stop polling after 2 minutes
    setTimeout(() => {
      if (interval) clearInterval(interval)
      if (step === 'checking') {
        setError('Verification timed out. Please check your transaction manually.')
        setStep('payment')
      }
    }, 120000)
  }

  const reset = () => {
    if (pollingInterval) clearInterval(pollingInterval)
    setStep('setup')
    setMerchant(null)
    setOrder(null)
    setOrderId('')
    setTxHash('')
    setError('')
    setPollingInterval(null)
  }

  const handleChainChange = (newChain: string) => {
    setChain(newChain)
    // Auto-fill common USDT contract addresses
    switch (newChain) {
      case 'BSC':
        setTokenContract('0x55d398326f99059ff775485246999027b3197955') // BSC-USD BEP-20
        break
      case 'TRC-20':
        setTokenContract('TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t') // USDT TRC-20
        break
      case 'ERC-20':
        setTokenContract('0xdAC17F958D2ee523a2206206994597C13D831ec7') // USDT ERC-20
        break
      default:
        setTokenContract('')
    }
  }

  const copyToClipboard = (text: string) => {
    navigator.clipboard.writeText(text)
  }

  // Generate proper QR code data for token payments
  const getQRCodeValue = () => {
    if (!order) return ''
    
    // Just return the wallet address for Binance scanner compatibility
    return order.deposit_address
  }

  return (
    <div style={{ maxWidth: 700, margin: '0 auto', padding: 20, fontFamily: 'Arial, sans-serif' }}>
      <h1 style={{ textAlign: 'center', color: '#333' }}>🔥 Real Crypto Payment Test</h1>
      <p style={{ textAlign: 'center', color: '#666', marginBottom: 30 }}>
        Test with your actual wallet address and real cryptocurrency transactions
      </p>
      
      {error && (
        <div style={{ 
          background: '#ffe6e6', 
          padding: 15, 
          marginBottom: 20, 
          borderRadius: 8, 
          color: '#d63384',
          border: '1px solid #f5c6cb'
        }}>
          <strong>⚠️ Error:</strong> {error}
        </div>
      )}

      {/* Setup */}
      {step === 'setup' && (
        <div style={{ padding: 30, background: '#f8f9fa', borderRadius: 12, border: '1px solid #dee2e6' }}>
          <h2 style={{ marginTop: 0, color: '#495057' }}>🛠️ Setup Real Payment Test</h2>
          
          <div style={{ marginBottom: 20 }}>
            <label style={{ display: 'block', marginBottom: 8, fontWeight: 'bold', color: '#495057' }}>
              Your Wallet Address (Merchant):
            </label>
            <input 
              value={merchantWallet}
              onChange={e => setMerchantWallet(e.target.value)}
              placeholder="0x... (your actual wallet address)"
              style={{ 
                width: '100%', 
                padding: 12, 
                fontSize: 14, 
                borderRadius: 6,
                border: '1px solid #ced4da',
                fontFamily: 'monospace'
              }}
            />
            <small style={{ color: '#6c757d' }}>
              This is where you'll receive the payment. Use your real wallet address.
            </small>
          </div>

          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20, marginBottom: 20 }}>
            <div>
              <label style={{ display: 'block', marginBottom: 8, fontWeight: 'bold', color: '#495057' }}>
                Amount (USDT):
              </label>
              <input 
                type="number"
                value={amount}
                onChange={e => setAmount(Number(e.target.value))}
                style={{ 
                  width: '100%', 
                  padding: 12, 
                  fontSize: 16, 
                  textAlign: 'center',
                  borderRadius: 6,
                  border: '1px solid #ced4da'
                }}
                step="0.000001"
                min="0.000001"
              />
            </div>
            
            <div>
              <label style={{ display: 'block', marginBottom: 8, fontWeight: 'bold', color: '#495057' }}>
                Blockchain:
              </label>
              <select 
                value={chain}
                onChange={e => handleChainChange(e.target.value)}
                style={{ 
                  width: '100%', 
                  padding: 12, 
                  fontSize: 16,
                  borderRadius: 6,
                  border: '1px solid #ced4da'
                }}
              >
                <option value="BSC">BSC (BEP-20)</option>
                <option value="TRC-20">TRC-20 (Tron)</option>
                <option value="ERC-20">ERC-20 (Ethereum)</option>
              </select>
            </div>
          </div>

          <div style={{ marginBottom: 20 }}>
            <label style={{ display: 'block', marginBottom: 8, fontWeight: 'bold', color: '#495057' }}>
              Token Contract Address:
            </label>
            <input 
              value={tokenContract}
              onChange={e => setTokenContract(e.target.value)}
              placeholder="0x... or TR... (USDT contract address)"
              style={{ 
                width: '100%', 
                padding: 12, 
                fontSize: 14, 
                borderRadius: 6,
                border: '1px solid #ced4da',
                fontFamily: 'monospace'
              }}
            />
            <small style={{ color: '#6c757d' }}>
              {chain === 'BSC' && 'BSC-USD: 0x55d398326f99059ff775485246999027b3197955'}
              {chain === 'TRC-20' && 'TRC-20 USDT: TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t'}
              {chain === 'ERC-20' && 'ERC-20 USDT: 0xdAC17F958D2ee523a2206206994597C13D831ec7'}
            </small>
          </div>
          
          <div style={{ textAlign: 'center' }}>
            <button 
              onClick={createMerchantAndOrder}
              disabled={loading || !merchantWallet.trim()}
              style={{ 
                padding: '15px 30px', 
                background: loading || !merchantWallet.trim() ? '#6c757d' : '#dc3545', 
                color: 'white', 
                border: 'none', 
                borderRadius: 8, 
                cursor: loading || !merchantWallet.trim() ? 'not-allowed' : 'pointer',
                fontSize: 16,
                fontWeight: 'bold'
              }}
            >
              {loading ? '⏳ Setting up...' : '🚀 Create Real Payment Request'}
            </button>
          </div>
        </div>
      )}

      {/* Payment */}
      {step === 'payment' && order && merchant && (
        <div>
          <div style={{ textAlign: 'center', marginBottom: 30 }}>
            <h2 style={{ color: '#495057' }}>💰 Send Real Payment</h2>
            <div style={{ 
              background: 'white', 
              padding: 20, 
              borderRadius: 12, 
              border: '2px solid #28a745',
              display: 'inline-block',
              boxShadow: '0 4px 8px rgba(0,0,0,0.1)'
            }}>
              <QRCode value={getQRCodeValue()} size={250} level="H" />
            </div>
          </div>

          <div style={{ background: '#e8f5e8', padding: 25, borderRadius: 12, marginBottom: 25, border: '1px solid #c3e6cb' }}>
            <h3 style={{ marginTop: 0, color: '#155724' }}>💎 Payment Details</h3>
            
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20, marginBottom: 20 }}>
              <div>
                <strong>Amount:</strong>
                <div style={{ fontSize: 24, color: '#28a745', fontWeight: 'bold' }}>
                  {(order.amount_minor / 1000000000000000000).toFixed(6)} USDT
                </div>
              </div>
              <div>
                <strong>Network:</strong>
                <div style={{ fontSize: 18, color: '#007bff', fontWeight: 'bold' }}>
                  {order.chain === 'BSC' ? 'BSC (BEP-20)' : order.chain}
                </div>
                {tokenContract && (
                  <div style={{ fontSize: 12, color: '#6c757d' }}>
                    Token Contract: {tokenContract}
                  </div>
                )}
              </div>
            </div>

            <div style={{ marginBottom: 15 }}>
              <strong>Send To Address:</strong>
              <div style={{ 
                fontFamily: 'monospace', 
                fontSize: 13, 
                wordBreak: 'break-all',
                background: 'white',
                padding: 12,
                marginTop: 8,
                borderRadius: 6,
                border: '1px solid #ced4da',
                position: 'relative'
              }}>
                {order.deposit_address}
                <button 
                  onClick={() => copyToClipboard(order.deposit_address)}
                  style={{ 
                    position: 'absolute',
                    right: 8,
                    top: 8,
                    padding: '4px 8px', 
                    background: '#007bff', 
                    color: 'white', 
                    border: 'none', 
                    borderRadius: 4, 
                    fontSize: 11,
                    cursor: 'pointer'
                  }}
                >
                  📋 Copy
                </button>
              </div>
            </div>

            <div style={{ background: '#fff3cd', padding: 15, borderRadius: 8, border: '1px solid #ffeaa7' }}>
              <strong>⚠️ Important:</strong>
              <ul style={{ margin: '10px 0 0 0', paddingLeft: 20 }}>
                <li>Send exactly <strong>{(order.amount_minor / 1000000000000000000).toFixed(6)} USDT</strong></li>
                <li>Use <strong>{order.chain === 'BSC' ? 'BSC (BEP-20)' : order.chain}</strong> network</li>
                <li>Token: <strong>USDT {order.chain === 'BSC' ? 'BEP-20' : order.chain}</strong></li>
                <li>Send to the address above (your merchant wallet: {merchant.merchant_wallet_address.substring(0, 8)}...)</li>
                <li>Copy transaction hash from your wallet after sending</li>
              </ul>
            </div>
          </div>

          <div style={{ marginBottom: 25 }}>
            <label style={{ display: 'block', marginBottom: 10, fontWeight: 'bold', color: '#495057' }}>
              Paste Transaction Hash:
            </label>
            <input 
              value={txHash}
              onChange={e => setTxHash(e.target.value)}
              placeholder="0x... (copy from your wallet after sending)"
              style={{ 
                width: '100%', 
                padding: 15, 
                borderRadius: 8,
                border: '2px solid #ced4da',
                fontFamily: 'monospace',
                fontSize: 14
              }}
            />
          </div>

          <div style={{ textAlign: 'center' }}>
            <button 
              onClick={submitTransaction}
              disabled={loading || !txHash.trim()}
              style={{ 
                padding: '15px 40px', 
                background: loading || !txHash.trim() ? '#6c757d' : '#28a745', 
                color: 'white', 
                border: 'none', 
                borderRadius: 8, 
                cursor: loading || !txHash.trim() ? 'not-allowed' : 'pointer',
                fontSize: 16,
                fontWeight: 'bold'
              }}
            >
              {loading ? '⏳ Submitting...' : '🔍 Verify Real Transaction'}
            </button>
          </div>
        </div>
      )}

      {/* Checking */}
      {step === 'checking' && order && (
        <div style={{ textAlign: 'center', padding: 40, background: '#fff3cd', borderRadius: 12, border: '1px solid #ffeaa7' }}>
          <h2 style={{ color: '#856404' }}>🔍 Verifying Real Transaction</h2>
          <div style={{ 
            display: 'inline-block', 
            width: 60, 
            height: 60, 
            border: '6px solid #f3f3f3', 
            borderTop: '6px solid #ffc107', 
            borderRadius: '50%', 
            animation: 'spin 1s linear infinite',
            marginBottom: 20
          }} />
          <p style={{ fontSize: 18, color: '#856404' }}>
            🔗 Checking {order.chain} blockchain for your transaction...
          </p>
          <p style={{ fontSize: 14, color: '#6c757d' }}>
            Current Status: <strong>{order.status}</strong>
          </p>
          <p style={{ fontSize: 12, color: '#6c757d' }}>
            This may take 30 seconds to 2 minutes depending on network confirmation
          </p>
        </div>
      )}

      {/* Complete */}
      {step === 'complete' && order && (
        <div style={{ textAlign: 'center', padding: 40, background: '#d4edda', borderRadius: 12, border: '2px solid #28a745' }}>
          <h2 style={{ color: '#155724' }}>🎉 Real Payment Successful!</h2>
          <div style={{ fontSize: 60, marginBottom: 20 }}>✅</div>
          
          <div style={{ 
            background: 'white', 
            padding: 25, 
            borderRadius: 8, 
            marginBottom: 25, 
            textAlign: 'left',
            maxWidth: 500,
            margin: '0 auto 25px'
          }}>
            <div style={{ marginBottom: 15 }}>
              <strong>Amount Received:</strong> 
              <span style={{ color: '#28a745', fontSize: 20, marginLeft: 10 }}>
                {(order.amount_minor / 1000000000000000000).toFixed(6)} USDT
              </span>
            </div>
            <div style={{ marginBottom: 15 }}>
              <strong>Network:</strong> {order.chain}
            </div>
            <div style={{ marginBottom: 15 }}>
              <strong>Status:</strong> 
              <span style={{ color: '#28a745', fontWeight: 'bold', marginLeft: 10 }}>
                {order.status}
              </span>
            </div>
            {order.tx_hash && (
              <div style={{ marginBottom: 15 }}>
                <strong>Transaction Hash:</strong>
                <div style={{ 
                  fontSize: 11, 
                  fontFamily: 'monospace', 
                  wordBreak: 'break-all',
                  marginTop: 5,
                  color: '#6c757d'
                }}>
                  {order.tx_hash}
                </div>
              </div>
            )}
            {order.paid_at && (
              <div>
                <strong>Confirmed At:</strong> {new Date(order.paid_at).toLocaleString()}
              </div>
            )}
          </div>
          
          <button 
            onClick={reset}
            style={{ 
              padding: '15px 30px', 
              background: '#6c757d', 
              color: 'white', 
              border: 'none', 
              borderRadius: 8, 
              cursor: 'pointer',
              fontSize: 16,
              fontWeight: 'bold'
            }}
          >
            🔄 Test Another Payment
          </button>
        </div>
      )}

      <style>{`
        @keyframes spin {
          0% { transform: rotate(0deg); }
          100% { transform: rotate(360deg); }
        }
      `}</style>
    </div>
  )
}