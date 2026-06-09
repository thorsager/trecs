package proto

const (
	// SIPIndicator is the prefix used in SIP version strings (e.g. "SIP/2.0").
	SIPIndicator = "SIP/"
	// SIPVersion is the SIP protocol version string used in start lines.
	SIPVersion = SIPIndicator + "2.0"

	SIPMethodINVITE   SIPMethod = "INVITE"
	SIPMethodACK      SIPMethod = "ACK"
	SIPMethodBYE      SIPMethod = "BYE"
	SIPMethodCANCEL   SIPMethod = "CANCEL"
	SIPMethodREGISTER SIPMethod = "REGISTER"
	SIPMethodOPTIONS  SIPMethod = "OPTIONS"

	// Extension methods.
	SIPMethodPRACK     SIPMethod = "PRACK"     // RFC 3262
	SIPMethodSUBSCRIBE SIPMethod = "SUBSCRIBE" // RFC 6665
	SIPMethodNOTIFY    SIPMethod = "NOTIFY"    // RFC 6665
	SIPMethodPUBLISH   SIPMethod = "PUBLISH"   // RFC 3903
	SIPMethodINFO      SIPMethod = "INFO"      // RFC 6086
	SIPMethodREFER     SIPMethod = "REFER"     // RFC 3515
	SIPMethodMESSAGE   SIPMethod = "MESSAGE"   // RFC 3428
	SIPMethodUPDATE    SIPMethod = "UPDATE"    // RFC 3311

	SIPStatusTrying             = 100 // RFC 3261
	SIPStatusRinging            = 180 // RFC 3261
	SIPStatusCallBeingForwarded = 181 // RFC 3261
	SIPStatusQueued             = 182 // RFC 3261
	SIPStatusSessionProgress    = 183 // RFC 3261

	SIPStatusOK = 200 // RFC 3261

	SIPStatusMultipleChoices    = 300 // RFC 3261
	SIPStatusMovedPermanently   = 301 // RFC 3261
	SIPStatusMovedTemporarily   = 302 // RFC 3261
	SIPStatusUseProxy           = 305 // RFC 3261
	SIPStatusAlternativeService = 380 // RFC 3261

	SIPStatusBadRequest                  = 400 // RFC 3261
	SIPStatusUnauthorized                = 401 // RFC 3261
	SIPStatusPaymentRequired             = 402 // RFC 3261
	SIPStatusForbidden                   = 403 // RFC 3261
	SIPStatusNotFound                    = 404 // RFC 3261
	SIPStatusMethodNotAllowed            = 405 // RFC 3261
	SIPStatusNotAcceptable               = 406 // RFC 3261
	SIPStatusProxyAuthenticationRequired = 407 // RFC 3261
	SIPStatusRequestTimeout              = 408 // RFC 3261
	SIPStatusGone                        = 410 // RFC 3261
	SIPStatusRequestEntityTooLarge       = 413 // RFC 3261
	SIPStatusRequestURITooLong           = 414 // RFC 3261
	SIPStatusUnsupportedMediaType        = 415 // RFC 3261
	SIPStatusUnsupportedURIScheme        = 416 // RFC 3261
	SIPStatusBadExtension                = 420 // RFC 3261
	SIPStatusExtensionRequired           = 421 // RFC 3261
	SIPStatusIntervalTooBrief            = 423 // RFC 3261
	SIPStatusTemporarilyUnavailable      = 480 // RFC 3261
	SIPStatusCallTransactionDoesNotExist = 481 // RFC 3261
	SIPStatusLoopDetected                = 482 // RFC 3261
	SIPStatusTooManyHops                 = 483 // RFC 3261
	SIPStatusAddressIncomplete           = 484 // RFC 3261
	SIPStatusAmbiguous                   = 485 // RFC 3261
	SIPStatusBusyHere                    = 486 // RFC 3261
	SIPStatusRequestTerminated           = 487 // RFC 3261
	SIPStatusNotAcceptableHere           = 488 // RFC 3261
	SIPStatusBadEvent                    = 489 // RFC 3265
	SIPStatusRequestPending              = 491 // RFC 3261
	SIPStatusUndecipherable              = 493 // RFC 3261

	SIPStatusServerInternalError = 500 // RFC 3261
	SIPStatusNotImplemented      = 501 // RFC 3261
	SIPStatusBadGateway          = 502 // RFC 3261
	SIPStatusServiceUnavailable  = 503 // RFC 3261
	SIPStatusServerTimeout       = 504 // RFC 3261
	SIPStatusVersionNotSupported = 505 // RFC 3261
	SIPStatusMessageTooLarge     = 513 // RFC 3261

	SIPStatusBusyEverywhere       = 600 // RFC 3261
	SIPStatusDecline              = 603 // RFC 3261
	SIPStatusDoesNotExistAnywhere = 604 // RFC 3261
	SIPStatusNotAcceptableGlobal  = 606 // RFC 3261
)
