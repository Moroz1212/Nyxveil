#import <Foundation/Foundation.h>
NS_ASSUME_NONNULL_BEGIN
@interface NVCore : NSObject
- (instancetype)initWithPacket:(void (^)(NSData *))packet failure:(void (^)(NSString *))failure NS_SWIFT_NAME(init(packet:failure:));
- (nullable NSString *)begin:(NSString *)config error:(NSError **)error NS_SWIFT_NAME(begin(_:));
- (BOOL)send:(NSData *)packet error:(NSError **)error NS_SWIFT_NAME(send(_:));
- (void)close;
- (NSString *)stats NS_SWIFT_NAME(stats());
+ (BOOL)verify:(NSData *)catalog keys:(NSData *)keys error:(NSError **)error NS_SWIFT_NAME(verify(_:keys:));
@end
NS_ASSUME_NONNULL_END
