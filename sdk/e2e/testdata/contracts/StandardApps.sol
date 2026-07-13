// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

library StandardAssetTransfer {
    address private constant SATOSHINET_ASSET =
        address(0x0000000000000000000000000000000000534E01);

    function balanceOf(address owner, string memory assetName) internal view returns (string memory) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature("balanceOf(address,string)", owner, assetName)
        );
        require(ok, "asset balance failed");
        return string(readRawDynamicBytes(ret));
    }

    function fundingAssetAmount(string memory assetName) internal view returns (string memory) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature("fundingAssetAmount(string)", assetName)
        );
        require(ok, "funding asset failed");
        return string(readRawDynamicBytes(ret));
    }

    function fundingAssetCount() internal view returns (uint256) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature("fundingAssetCount()")
        );
        require(ok, "funding asset count failed");
        return abi.decode(ret, (uint256));
    }

    function claimFundingAsset(string memory assetName, string memory amount) internal {
        require(isPositive(amount), "claim amount required");
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.call(
            abi.encodeWithSignature("claimFundingAsset(string,string)", assetName, amount)
        );
        require(ok && (ret.length == 0 || abi.decode(ret, (bool))), "claim funding failed");
    }

    function callerAddress() internal view returns (string memory) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature("callerAddress()")
        );
        require(ok, "caller address failed");
        return string(readRawDynamicBytes(ret));
    }

    function fundingSats() internal view returns (uint256) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature("fundingSats()")
        );
        require(ok, "funding sats failed");
        return abi.decode(ret, (uint256));
    }

    function transferAsset(string memory assetName, string memory recipient, string memory amount) internal {
        require(compareAmount(amount, "0") > 0, "amount required");
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.call(
            abi.encodeWithSignature("transferAsset(string,string,string,bytes)", assetName, recipient, amount, "")
        );
        require(ok && (ret.length == 0 || abi.decode(ret, (bool))), "asset transfer failed");
    }

    function transferAssets(
        string[] memory assetNames,
        string[] memory recipients,
        string[] memory amounts,
        bytes[] memory extraData
    ) internal {
        require(
            assetNames.length == recipients.length &&
            assetNames.length == amounts.length &&
            assetNames.length == extraData.length,
            "transfer arrays"
        );
        require(assetNames.length > 0, "transfer required");
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.call(
            abi.encodeWithSignature(
                "transferAssets(string[],string[],string[],bytes[])",
                assetNames,
                recipients,
                amounts,
                extraData
            )
        );
        require(ok && (ret.length == 0 || abi.decode(ret, (bool))), "asset batch transfer failed");
    }

    function compareAmount(string memory left, string memory right) internal view returns (int256) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature("compareAmount(string,string)", left, right)
        );
        require(ok, "amount compare failed");
        return abi.decode(ret, (int256));
    }

    function addAmount(string memory left, string memory right) internal view returns (string memory) {
        return amountOp("addAmount(string,string)", left, right);
    }

    function subAmount(string memory left, string memory right) internal view returns (string memory) {
        return amountOp("subAmount(string,string)", left, right);
    }

    function mulAmount(string memory left, string memory right) internal view returns (string memory) {
        return amountOp("mulAmount(string,string)", left, right);
    }

    function divAmount(string memory left, string memory right) internal view returns (string memory) {
        return amountOp("divAmount(string,string)", left, right);
    }

    function amountOp(string memory signature, string memory left, string memory right) private view returns (string memory) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature(signature, left, right)
        );
        require(ok, "amount op failed");
        return string(readRawDynamicBytes(ret));
    }

    function readRawDynamicBytes(bytes memory data) internal pure returns (bytes memory) {
        require(data.length >= 32, "bad response");
        uint256 size;
        assembly {
            size := mload(add(data, 32))
        }
        require(data.length >= 32 + size, "short response");
        bytes memory out = new bytes(size);
        for (uint256 i = 0; i < size; i++) {
            out[i] = data[32 + i];
        }
        return out;
    }

    function uintToString(uint256 value) internal view returns (string memory) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature("uintToAmount(uint256)", value)
        );
        require(ok, "uint amount failed");
        return string(readRawDynamicBytes(ret));
    }

    function stringToUintFloor(string memory value) internal view returns (uint256) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature("amountToUintFloor(string)", value)
        );
        require(ok, "amount uint failed");
        return abi.decode(ret, (uint256));
    }

    function stringToUintCeil(string memory value) internal view returns (uint256) {
        (bool ok, bytes memory ret) = SATOSHINET_ASSET.staticcall(
            abi.encodeWithSignature("amountToUintCeil(string)", value)
        );
        require(ok, "amount ceil failed");
        return abi.decode(ret, (uint256));
    }

    function isPositive(string memory value) internal view returns (bool) {
        return compareAmount(value, "0") > 0;
    }
}

interface ISatoshiNetContractInfo {
    function contractName() external view returns (string memory);
    function contractSubtype() external view returns (string memory);
    function managedAssetCount() external view returns (uint256);
    function managedAsset(uint256 index) external view returns (string memory);
    function managedAssetBalance(uint256 index) external view returns (string memory);
    function managedAssetBalance(string calldata assetName) external view returns (string memory);
}

abstract contract SatoshiNetContractInfo is ISatoshiNetContractInfo {
    string private _contractName;
    string private _contractSubtype;
    string[] private _managedAssets;

    constructor(string memory name_, string memory subtype_, string[] memory managedAssets_) {
        _contractName = name_;
        _contractSubtype = subtype_;
        for (uint256 i = 0; i < managedAssets_.length; i++) {
            _managedAssets.push(managedAssets_[i]);
        }
    }

    function contractName() external view returns (string memory) {
        return _contractName;
    }

    function contractSubtype() external view returns (string memory) {
        return _contractSubtype;
    }

    function managedAssetCount() external view returns (uint256) {
        return _managedAssets.length;
    }

    function managedAsset(uint256 index) external view returns (string memory) {
        require(index < _managedAssets.length, "asset index");
        return _managedAssets[index];
    }

    function managedAssetBalance(uint256 index) external view returns (string memory) {
        require(index < _managedAssets.length, "asset index");
        return _managedAssetBalance(_managedAssets[index]);
    }

    function managedAssetBalance(string calldata assetName) external view returns (string memory) {
        return _managedAssetBalance(assetName);
    }

    function _managedAssetBalance(string memory assetName) internal view virtual returns (string memory);
}

contract ConstantProductAMM is SatoshiNetContractInfo {
    string private constant SATS_ASSET = "::";

    string public assetName;
    string public assetReserve;
    uint256 public satReserve;
    uint256 public totalLiquidity;
    mapping(address => uint256) public liquidityOf;
    mapping(address => bool) private knownLiquidityProvider;
    mapping(address => string) private liquidityRecipientOf;
    address[] private liquidityProviders;

    constructor(string memory assetName_) SatoshiNetContractInfo("amm", "amm", ammManagedAssets(assetName_)) {
        require(bytes(assetName_).length != 0, "asset required");
        assetName = assetName_;
        assetReserve = "0";
    }

    function ammManagedAssets(string memory assetName_) private pure returns (string[] memory assets) {
        assets = new string[](2);
        assets[0] = assetName_;
        assets[1] = SATS_ASSET;
    }

    receive() external payable {
        _defaultSwap();
    }

    fallback() external payable {
        revert("unsupported calldata");
    }

    function _defaultSwap() private {
        uint256 satIn = StandardAssetTransfer.fundingSats();
        string memory fundedAsset = StandardAssetTransfer.fundingAssetAmount(assetName);
        bool hasSats = satIn > 0;
        bool hasAsset = StandardAssetTransfer.isPositive(fundedAsset);
        uint256 knownFundingCount = (hasSats ? 1 : 0) + (hasAsset ? 1 : 0);
        require(knownFundingCount == 1, "default input required");
        require(StandardAssetTransfer.fundingAssetCount() == knownFundingCount, "unsupported default funding");

        if (hasSats) {
            _swapSatForAsset("0");
            return;
        }
        _swapAssetForSat(0);
    }

    function addLiquidity(uint256 minLiquidity) external returns (uint256) {
        string memory fundedAsset = StandardAssetTransfer.fundingAssetAmount(assetName);
        uint256 assetIn = StandardAssetTransfer.stringToUintFloor(fundedAsset);
        uint256 satIn = StandardAssetTransfer.fundingSats();
        require(assetIn > 0 && satIn > 0, "liquidity required");
        StandardAssetTransfer.claimFundingAsset(assetName, StandardAssetTransfer.uintToString(assetIn));

        uint256 liquidity;
        uint256 currentAssetReserve = StandardAssetTransfer.stringToUintFloor(assetReserve);
        if (totalLiquidity == 0) {
            liquidity = sqrt(assetIn * satIn);
        } else {
            require(currentAssetReserve > 0 && satReserve > 0, "empty reserves");
            liquidity = min((assetIn * totalLiquidity) / currentAssetReserve, (satIn * totalLiquidity) / satReserve);
        }
        require(liquidity >= minLiquidity && liquidity > 0, "insufficient liquidity minted");

        assetReserve = StandardAssetTransfer.addAmount(assetReserve, StandardAssetTransfer.uintToString(assetIn));
        satReserve += satIn;
        totalLiquidity += liquidity;
        if (!knownLiquidityProvider[msg.sender]) {
            knownLiquidityProvider[msg.sender] = true;
            liquidityProviders.push(msg.sender);
        }
        liquidityRecipientOf[msg.sender] = StandardAssetTransfer.callerAddress();
        liquidityOf[msg.sender] += liquidity;

        require(StandardAssetTransfer.compareAmount(StandardAssetTransfer.balanceOf(address(this), assetName), assetReserve) >= 0, "unfunded asset");
        require(StandardAssetTransfer.compareAmount(StandardAssetTransfer.balanceOf(address(this), SATS_ASSET), StandardAssetTransfer.uintToString(satReserve)) >= 0, "unfunded sats");
        return liquidity;
    }

    function removeLiquidity(
        uint256 liquidity,
        string calldata minAssetOut,
        uint256 minSatOut
    ) external returns (string memory assetOut, uint256 satOut) {
        string memory recipient = StandardAssetTransfer.callerAddress();
        require(liquidity > 0 && liquidityOf[msg.sender] >= liquidity, "insufficient liquidity");
        require(totalLiquidity > 0, "empty pool");

        assetOut = StandardAssetTransfer.uintToString((StandardAssetTransfer.stringToUintFloor(assetReserve) * liquidity) / totalLiquidity);
        satOut = (satReserve * liquidity) / totalLiquidity;
        require(StandardAssetTransfer.compareAmount(assetOut, minAssetOut) >= 0 && satOut >= minSatOut, "slippage");
        require(StandardAssetTransfer.isPositive(assetOut) && satOut > 0, "zero output");

        liquidityOf[msg.sender] -= liquidity;
        totalLiquidity -= liquidity;
        assetReserve = StandardAssetTransfer.subAmount(assetReserve, assetOut);
        satReserve -= satOut;
        StandardAssetTransfer.transferAsset(assetName, recipient, assetOut);
        StandardAssetTransfer.transferAsset(SATS_ASSET, recipient, StandardAssetTransfer.uintToString(satOut));
    }

    function close() external returns (bool) {
        if (totalLiquidity == 0) {
            return true;
        }
        _transferLiquidityOnClose(assetReserve, satReserve, totalLiquidity);
        for (uint256 i = 0; i < liquidityProviders.length; i++) {
            address provider = liquidityProviders[i];
            if (liquidityOf[provider] != 0) {
                liquidityOf[provider] = 0;
            }
        }
        assetReserve = "0";
        satReserve = 0;
        totalLiquidity = 0;
        return true;
    }

    function _transferLiquidityOnClose(
        string memory originalAssetReserve,
        uint256 originalSatReserve,
        uint256 originalTotalLiquidity
    ) private {
        uint256 transferCount = _closeLiquidityTransferCount(
            originalAssetReserve,
            originalSatReserve,
            originalTotalLiquidity
        );
        if (transferCount == 0) {
            return;
        }
        string[] memory assetNames = new string[](transferCount);
        string[] memory recipients = new string[](transferCount);
        string[] memory amounts = new string[](transferCount);
        bytes[] memory extraData = new bytes[](transferCount);
        _fillCloseLiquidityTransfers(
            originalAssetReserve,
            originalSatReserve,
            originalTotalLiquidity,
            assetNames,
            recipients,
            amounts
        );
        StandardAssetTransfer.transferAssets(assetNames, recipients, amounts, extraData);
    }

    function _closeLiquidityTransferCount(
        string memory originalAssetReserve,
        uint256 originalSatReserve,
        uint256 originalTotalLiquidity
    ) private view returns (uint256 transferCount) {
        for (uint256 i = 0; i < liquidityProviders.length; i++) {
            address provider = liquidityProviders[i];
            uint256 liquidity = liquidityOf[provider];
            if (liquidity == 0) {
                continue;
            }
            string memory assetOut = StandardAssetTransfer.uintToString(
                (StandardAssetTransfer.stringToUintFloor(originalAssetReserve) * liquidity) / originalTotalLiquidity
            );
            uint256 satOut = (originalSatReserve * liquidity) / originalTotalLiquidity;
            if (StandardAssetTransfer.isPositive(assetOut)) {
                transferCount++;
            }
            if (satOut > 0) {
                transferCount++;
            }
        }
    }

    function _fillCloseLiquidityTransfers(
        string memory originalAssetReserve,
        uint256 originalSatReserve,
        uint256 originalTotalLiquidity,
        string[] memory assetNames,
        string[] memory recipients,
        string[] memory amounts
    ) private view {
        uint256 transferIndex = 0;
        for (uint256 i = 0; i < liquidityProviders.length; i++) {
            address provider = liquidityProviders[i];
            uint256 liquidity = liquidityOf[provider];
            if (liquidity == 0) {
                continue;
            }
            string memory assetOut = StandardAssetTransfer.uintToString(
                (StandardAssetTransfer.stringToUintFloor(originalAssetReserve) * liquidity) / originalTotalLiquidity
            );
            uint256 satOut = (originalSatReserve * liquidity) / originalTotalLiquidity;
            if (StandardAssetTransfer.isPositive(assetOut)) {
                assetNames[transferIndex] = assetName;
                recipients[transferIndex] = liquidityRecipientOf[provider];
                amounts[transferIndex] = assetOut;
                transferIndex++;
            }
            if (satOut > 0) {
                assetNames[transferIndex] = SATS_ASSET;
                recipients[transferIndex] = liquidityRecipientOf[provider];
                amounts[transferIndex] = StandardAssetTransfer.uintToString(satOut);
                transferIndex++;
            }
        }
    }

    function swapSatForAsset(string calldata minAssetOut) external returns (string memory assetOut) {
        return _swapSatForAsset(minAssetOut);
    }

    function _swapSatForAsset(string memory minAssetOut) private returns (string memory assetOut) {
        string memory recipient = StandardAssetTransfer.callerAddress();
        uint256 satIn = StandardAssetTransfer.fundingSats();
        require(satIn > 0, "input required");
        require(StandardAssetTransfer.isPositive(assetReserve) && satReserve > 0, "empty pool");
        require(StandardAssetTransfer.compareAmount(StandardAssetTransfer.balanceOf(address(this), SATS_ASSET), StandardAssetTransfer.uintToString(satReserve + satIn)) >= 0, "unfunded swap");

        uint256 satInWithFee = satIn * 997;
        string memory numerator = StandardAssetTransfer.mulAmount(assetReserve, StandardAssetTransfer.uintToString(satInWithFee));
        string memory denominator = StandardAssetTransfer.uintToString(satReserve * 1000 + satInWithFee);
        assetOut = StandardAssetTransfer.uintToString(StandardAssetTransfer.stringToUintFloor(StandardAssetTransfer.divAmount(numerator, denominator)));
        require(StandardAssetTransfer.compareAmount(assetOut, minAssetOut) >= 0 && StandardAssetTransfer.isPositive(assetOut), "slippage");
        require(StandardAssetTransfer.compareAmount(StandardAssetTransfer.balanceOf(address(this), assetName), assetOut) >= 0, "insufficient asset");

        assetReserve = StandardAssetTransfer.subAmount(assetReserve, assetOut);
        satReserve += satIn;
        StandardAssetTransfer.transferAsset(assetName, recipient, assetOut);
    }

    function swapAssetForSat(uint256 minSatOut) external returns (uint256 satOut) {
        return _swapAssetForSat(minSatOut);
    }

    function _swapAssetForSat(uint256 minSatOut) private returns (uint256 satOut) {
        string memory recipient = StandardAssetTransfer.callerAddress();
        string memory fundedAsset = StandardAssetTransfer.fundingAssetAmount(assetName);
        uint256 assetIn = StandardAssetTransfer.stringToUintFloor(fundedAsset);
        require(assetIn > 0, "input required");
        StandardAssetTransfer.claimFundingAsset(assetName, StandardAssetTransfer.uintToString(assetIn));
        require(StandardAssetTransfer.isPositive(assetReserve) && satReserve > 0, "empty pool");

        string memory assetInWithFee = StandardAssetTransfer.uintToString(assetIn * 997);
        string memory numerator = StandardAssetTransfer.mulAmount(StandardAssetTransfer.uintToString(satReserve), assetInWithFee);
        string memory denominator = StandardAssetTransfer.addAmount(StandardAssetTransfer.mulAmount(assetReserve, "1000"), assetInWithFee);
        satOut = StandardAssetTransfer.stringToUintFloor(StandardAssetTransfer.divAmount(numerator, denominator));
        require(satOut >= minSatOut && satOut > 0, "slippage");
        require(StandardAssetTransfer.compareAmount(StandardAssetTransfer.balanceOf(address(this), SATS_ASSET), StandardAssetTransfer.uintToString(satOut)) >= 0, "insufficient sats");

        assetReserve = StandardAssetTransfer.addAmount(assetReserve, StandardAssetTransfer.uintToString(assetIn));
        satReserve -= satOut;
        StandardAssetTransfer.transferAsset(SATS_ASSET, recipient, StandardAssetTransfer.uintToString(satOut));
    }

    function liquidityOfCaller() external view returns (uint256) {
        return liquidityOf[msg.sender];
    }

    function reserves() external view returns (
        string memory asset,
        string memory assetAmount,
        uint256 satAmount,
        uint256 totalLpt
    ) {
        return (assetName, assetReserve, satReserve, totalLiquidity);
    }

    function _managedAssetBalance(string memory queryAsset) internal view override returns (string memory) {
        if (sameAsset(queryAsset, assetName)) {
            return assetReserve;
        }
        if (sameAsset(queryAsset, SATS_ASSET)) {
            return StandardAssetTransfer.uintToString(satReserve);
        }
        return "0";
    }

    function quoteSatForAsset(uint256 satIn) external view returns (string memory assetOut) {
        require(satIn > 0, "input required");
        require(StandardAssetTransfer.isPositive(assetReserve) && satReserve > 0, "empty pool");
        uint256 satInWithFee = satIn * 997;
        string memory numerator = StandardAssetTransfer.mulAmount(assetReserve, StandardAssetTransfer.uintToString(satInWithFee));
        string memory denominator = StandardAssetTransfer.uintToString(satReserve * 1000 + satInWithFee);
        return StandardAssetTransfer.uintToString(StandardAssetTransfer.stringToUintFloor(StandardAssetTransfer.divAmount(numerator, denominator)));
    }

    function quoteAssetForSat(string calldata assetIn) external view returns (uint256 satOut) {
        require(StandardAssetTransfer.isPositive(assetIn), "input required");
        require(StandardAssetTransfer.isPositive(assetReserve) && satReserve > 0, "empty pool");
        string memory assetInWithFee = StandardAssetTransfer.mulAmount(assetIn, "997");
        string memory numerator = StandardAssetTransfer.mulAmount(StandardAssetTransfer.uintToString(satReserve), assetInWithFee);
        string memory denominator = StandardAssetTransfer.addAmount(StandardAssetTransfer.mulAmount(assetReserve, "1000"), assetInWithFee);
        return StandardAssetTransfer.stringToUintFloor(StandardAssetTransfer.divAmount(numerator, denominator));
    }

    function quoteAddLiquidity(string calldata assetIn, uint256 satIn) external view returns (uint256 liquidity) {
        require(StandardAssetTransfer.isPositive(assetIn) && satIn > 0, "liquidity required");
        uint256 assetAmount = StandardAssetTransfer.stringToUintFloor(assetIn);
        if (totalLiquidity == 0) {
            return sqrt(assetAmount * satIn);
        }
        require(StandardAssetTransfer.isPositive(assetReserve) && satReserve > 0, "empty reserves");
        return min((assetAmount * totalLiquidity) / StandardAssetTransfer.stringToUintFloor(assetReserve), (satIn * totalLiquidity) / satReserve);
    }

    function quoteRemoveLiquidity(uint256 liquidity) external view returns (string memory assetOut, uint256 satOut) {
        require(liquidity > 0 && totalLiquidity > 0, "liquidity required");
        assetOut = StandardAssetTransfer.uintToString((StandardAssetTransfer.stringToUintFloor(assetReserve) * liquidity) / totalLiquidity);
        satOut = (satReserve * liquidity) / totalLiquidity;
    }

    function min(uint256 left, uint256 right) private pure returns (uint256) {
        return left < right ? left : right;
    }

    function sqrt(uint256 value) private pure returns (uint256) {
        if (value == 0) {
            return 0;
        }
        uint256 z = (value + 1) / 2;
        uint256 y = value;
        while (z < y) {
            y = z;
            z = (value / z + z) / 2;
        }
        return y;
    }

    function sameAsset(string memory left, string memory right) private pure returns (bool) {
        return keccak256(bytes(left)) == keccak256(bytes(right));
    }
}

contract LimitOrderBook is SatoshiNetContractInfo {
    string private constant SATS_ASSET = "::";
    uint256 private constant MAX_STATE_VIEW_ORDERS = 20;

    event OrderCreated(
        uint256 indexed orderId,
        address indexed maker,
        string makerRecipient,
        string sellAsset,
        string buyAsset,
        string sellAmount,
        string buyAmount
    );
    event OrderFilled(
        uint256 indexed orderId,
        address indexed taker,
        string takerRecipient,
        string paidIn,
        string sellOut,
        string sellRemaining,
        string buyRemaining
    );
    event OrderCancelled(uint256 indexed orderId, address indexed maker, string recipient, string refundAsset, string refundAmount);

    struct Order {
        address maker;
        string makerRecipient;
        string sellAsset;
        string buyAsset;
        string sellRemaining;
        string buyRemaining;
        bool active;
    }

    uint256 public nextOrderId = 1;
    mapping(uint256 => Order) public orders;

    constructor(string memory assetName_) SatoshiNetContractInfo("limitorder", "limitorder", orderManagedAssets(assetName_)) {
        require(bytes(assetName_).length != 0, "asset required");
    }

    function orderManagedAssets(string memory assetName_) private pure returns (string[] memory assets) {
        assets = new string[](2);
        assets[0] = assetName_;
        assets[1] = SATS_ASSET;
    }

    receive() external payable {
        revert("default unsupported");
    }

    fallback() external payable {
        revert("default unsupported");
    }

    function createOrder(
        string calldata sellAsset,
        string calldata buyAsset,
        string calldata buyAmount
    ) external returns (uint256 orderId) {
        require(!sameAsset(sellAsset, buyAsset), "same asset");
        require(StandardAssetTransfer.isPositive(buyAmount), "buy amount required");
        string memory makerRecipient = StandardAssetTransfer.callerAddress();

        string memory sellAmount = fundingAmount(sellAsset);
        require(StandardAssetTransfer.isPositive(sellAmount), "sell funding required");
        claimIfAsset(sellAsset, sellAmount);

        orderId = nextOrderId++;
        orders[orderId] = Order({
            maker: msg.sender,
            makerRecipient: makerRecipient,
            sellAsset: sellAsset,
            buyAsset: buyAsset,
            sellRemaining: sellAmount,
            buyRemaining: buyAmount,
            active: true
        });
        emit OrderCreated(orderId, msg.sender, makerRecipient, sellAsset, buyAsset, sellAmount, buyAmount);
    }

    function fillOrder(uint256 orderId) external returns (string memory sellOut, string memory paidIn) {
        string memory takerRecipient = StandardAssetTransfer.callerAddress();
        Order storage order = orders[orderId];
        require(order.active, "inactive order");

        paidIn = fundingAmount(order.buyAsset);
        require(StandardAssetTransfer.isPositive(paidIn), "payment required");
        require(StandardAssetTransfer.compareAmount(paidIn, order.buyRemaining) <= 0, "overfill");
        claimIfAsset(order.buyAsset, paidIn);

        sellOut = StandardAssetTransfer.divAmount(
            StandardAssetTransfer.mulAmount(order.sellRemaining, paidIn),
            order.buyRemaining
        );
        sellOut = StandardAssetTransfer.uintToString(StandardAssetTransfer.stringToUintFloor(sellOut));
        require(StandardAssetTransfer.isPositive(sellOut), "zero fill");

        order.sellRemaining = StandardAssetTransfer.subAmount(order.sellRemaining, sellOut);
        order.buyRemaining = StandardAssetTransfer.subAmount(order.buyRemaining, paidIn);
        if (!StandardAssetTransfer.isPositive(order.sellRemaining) || !StandardAssetTransfer.isPositive(order.buyRemaining)) {
            order.active = false;
        }

        StandardAssetTransfer.transferAsset(order.buyAsset, order.makerRecipient, paidIn);
        StandardAssetTransfer.transferAsset(order.sellAsset, takerRecipient, sellOut);
        emit OrderFilled(orderId, msg.sender, takerRecipient, paidIn, sellOut, order.sellRemaining, order.buyRemaining);
    }

    function cancelOrder(uint256 orderId) external returns (bool) {
        string memory recipient = StandardAssetTransfer.callerAddress();
        Order storage order = orders[orderId];
        require(order.active, "inactive order");
        require(msg.sender == order.maker, "only maker");

        string memory refund = order.sellRemaining;
        order.sellRemaining = "0";
        order.buyRemaining = "0";
        order.active = false;
        StandardAssetTransfer.transferAsset(order.sellAsset, recipient, refund);
        emit OrderCancelled(orderId, msg.sender, recipient, order.sellAsset, refund);
        return true;
    }

    function close() external returns (bool) {
        uint256 transferCount = 0;
        for (uint256 id = 1; id < nextOrderId; id++) {
            Order storage order = orders[id];
            if (order.active && StandardAssetTransfer.isPositive(order.sellRemaining)) {
                transferCount++;
            }
        }
        if (transferCount > 0) {
            string[] memory assetNames = new string[](transferCount);
            string[] memory recipients = new string[](transferCount);
            string[] memory amounts = new string[](transferCount);
            bytes[] memory extraData = new bytes[](transferCount);
            uint256 transferIndex = 0;
            for (uint256 id = 1; id < nextOrderId; id++) {
                Order storage order = orders[id];
                if (!order.active || !StandardAssetTransfer.isPositive(order.sellRemaining)) {
                    continue;
                }
                assetNames[transferIndex] = order.sellAsset;
                recipients[transferIndex] = order.makerRecipient;
                amounts[transferIndex] = order.sellRemaining;
                transferIndex++;
            }
            StandardAssetTransfer.transferAssets(assetNames, recipients, amounts, extraData);
        }
        for (uint256 id = 1; id < nextOrderId; id++) {
            Order storage order = orders[id];
            if (!order.active) {
                continue;
            }
            order.sellRemaining = "0";
            order.buyRemaining = "0";
            order.active = false;
        }
        return true;
    }

    function activeOrderCount() external view returns (uint256 count) {
        for (uint256 id = 1; id < nextOrderId; id++) {
            if (orders[id].active) {
                count++;
            }
        }
    }

    function activeOrderId(uint256 activeIndex) external view returns (uint256) {
        uint256 seen = 0;
        for (uint256 id = 1; id < nextOrderId; id++) {
            if (!orders[id].active) {
                continue;
            }
            if (seen == activeIndex) {
                return id;
            }
            seen++;
        }
        revert("active order index");
    }

    function orderInfo(uint256 orderId) external view returns (
        address maker,
        string memory makerRecipient,
        string memory sellAsset,
        string memory buyAsset,
        string memory sellRemaining,
        string memory buyRemaining,
        bool active
    ) {
        Order storage order = orders[orderId];
        return (
            order.maker,
            order.makerRecipient,
            order.sellAsset,
            order.buyAsset,
            order.sellRemaining,
            order.buyRemaining,
            order.active
        );
    }

    function stateView() external view returns (string memory) {
        uint256 activeCount = 0;
        for (uint256 id = 1; id < nextOrderId; id++) {
            if (orders[id].active) {
                activeCount++;
            }
        }
        string memory out = string.concat(
            '{"nextOrderId":',
            StandardAssetTransfer.uintToString(nextOrderId),
            ',"activeOrderCount":',
            StandardAssetTransfer.uintToString(activeCount),
            ',"activeOrders":['
        );
        bool first = true;
        uint256 emitted = 0;
        for (uint256 id = 1; id < nextOrderId; id++) {
            if (emitted >= MAX_STATE_VIEW_ORDERS) {
                break;
            }
            Order storage order = orders[id];
            if (!order.active) {
                continue;
            }
            if (!first) {
                out = string.concat(out, ",");
            }
            first = false;
            out = string.concat(
                out,
                '{"orderId":',
                StandardAssetTransfer.uintToString(id),
                ',"maker":"',
                order.makerRecipient,
                '","sellAsset":"',
                order.sellAsset,
                '","buyAsset":"',
                order.buyAsset,
                '","sellRemaining":"',
                order.sellRemaining,
                '","buyRemaining":"',
                order.buyRemaining,
                '"}'
            );
            emitted++;
        }
        return string.concat(out, "]}");
    }

    function quoteFillOrder(uint256 orderId, string calldata paidIn) external view returns (string memory sellOut) {
        Order storage order = orders[orderId];
        require(order.active, "inactive order");
        require(StandardAssetTransfer.isPositive(paidIn), "payment required");
        require(StandardAssetTransfer.compareAmount(paidIn, order.buyRemaining) <= 0, "overfill");
        sellOut = StandardAssetTransfer.divAmount(
            StandardAssetTransfer.mulAmount(order.sellRemaining, paidIn),
            order.buyRemaining
        );
        return StandardAssetTransfer.uintToString(StandardAssetTransfer.stringToUintFloor(sellOut));
    }

    function _managedAssetBalance(string memory assetName) internal view override returns (string memory) {
        string memory total = "0";
        for (uint256 id = 1; id < nextOrderId; id++) {
            Order storage order = orders[id];
            if (order.active && sameAsset(order.sellAsset, assetName)) {
                total = StandardAssetTransfer.addAmount(total, order.sellRemaining);
            }
        }
        return total;
    }

    function fundingAmount(string memory assetName) private view returns (string memory) {
        if (sameAsset(assetName, SATS_ASSET)) {
            return StandardAssetTransfer.uintToString(StandardAssetTransfer.fundingSats());
        }
        return StandardAssetTransfer.fundingAssetAmount(assetName);
    }

    function claimIfAsset(string memory assetName, string memory amount) private {
        if (!sameAsset(assetName, SATS_ASSET)) {
            StandardAssetTransfer.claimFundingAsset(assetName, amount);
        }
    }

    function sameAsset(string memory left, string memory right) private pure returns (bool) {
        return keccak256(bytes(left)) == keccak256(bytes(right));
    }
}
